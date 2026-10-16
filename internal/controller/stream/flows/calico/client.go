// Copyright 2026 Illumio, Inc. All Rights Reserved.

package calico

import (
	"context"
	"fmt"
	"math"
	"net/netip"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	goldmanepb "github.com/illumio/cloud-operator/api/illumio/cloud/goldmane/v1"
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/goldmane"
)

// aggregationIntervalSeconds is the Goldmane stream aggregation interval. The Goldmane API
// requires it to always be 15s.
const aggregationIntervalSeconds = 15

// Protocol names as reported in a Goldmane FlowKey (felix/collector/utils.ProtoToString).
const (
	goldmaneProtoTCP   = "tcp"
	goldmaneProtoUDP   = "udp"
	goldmaneProtoSCTP  = "sctp"
	goldmaneProtoICMP  = "icmp"
	goldmaneProtoICMP6 = "icmp6"
)

// calicoClient collects flows from Calico Goldmane.
type calicoClient struct {
	logger          *zap.Logger
	flowSink        collector.FlowSink
	calicoNamespace string
	k8sClient       collector.K8sClientGetter
}

// connect discovers the Goldmane service, loads its mTLS credentials, and dials it.
func (c *calicoClient) connect(ctx context.Context) (*grpc.ClientConn, error) {
	clientset := c.k8sClient.GetClientset()

	service, err := goldmane.DiscoverGoldmane(ctx, c.calicoNamespace, clientset, c.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to discover Goldmane: %w", err)
	}

	tlsConfig, err := goldmane.GetTLSConfig(ctx, clientset, c.logger, c.calicoNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get Goldmane TLS config: %w", err)
	}

	conn, err := goldmane.ConnectToGoldmane(c.logger, goldmane.GetAddressFromService(service), tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Goldmane: %w", err)
	}

	return conn, nil
}

// Run connects to Goldmane, streams flows from it, and sends them to the flow sink as
// Calico flows. The connection is closed when Run returns, so each retry dials a
// fresh connection.
func (c *calicoClient) Run(ctx context.Context) error {
	conn, err := c.connect(ctx)
	if err != nil {
		c.logger.Warn("Failed to set up Goldmane connection; will retry", zap.Error(err))

		return err
	}

	defer func() {
		if err := conn.Close(); err != nil {
			c.logger.Debug("Failed to close Goldmane connection", zap.Error(err))
		}
	}()

	stream, err := goldmanepb.NewFlowsClient(conn).Stream(ctx, &goldmanepb.FlowStreamRequest{
		AggregationInterval: aggregationIntervalSeconds,
	})
	if err != nil {
		c.logger.Error("Error starting Goldmane flow stream", zap.Error(err))

		return err
	}

	warnedMissingIPs := false

	for {
		resp, err := stream.Recv()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			c.logger.Warn("Failed to receive flow from Goldmane stream", zap.Error(err))

			return err
		}

		flow := resp.GetFlow()
		if !warnedMissingIPs && flow != nil && (len(flow.GetSourceIps()) == 0 || len(flow.GetDestIps()) == 0) {
			warnedMissingIPs = true

			c.logger.Warn("Dropping Goldmane flows without source/destination IPs; " +
				"requires a Calico release that includes projectcalico/calico#13063 " +
				"with FelixConfiguration flowLogsGoldmaneIncludeIPs enabled")
		}

		calicoFlow := convertGoldmaneFlow(flow)
		if calicoFlow == nil {
			continue
		}

		c.flowSink.IncrementFlowsReceived()

		err = c.flowSink.CacheFlow(ctx, calicoFlow)
		if err != nil {
			c.logger.Error("Failed to cache Calico flow", zap.Error(err))

			return err
		}
	}
}

// convertGoldmaneFlow converts a Goldmane flow into a CalicoFlow. Returns nil if the flow
// lacks the IPs, protocol, port, or timestamp needed to identify its connections.
func convertGoldmaneFlow(flow *goldmanepb.Flow) *pb.CalicoFlow {
	key := flow.GetKey()
	if key == nil {
		return nil
	}

	sourceIPs := normalizeIPs(flow.GetSourceIps())
	destinationIPs := normalizeIPs(flow.GetDestIps())

	if len(sourceIPs) == 0 || len(destinationIPs) == 0 {
		return nil
	}

	destPort := key.GetDestPort()
	if destPort < 0 || destPort > math.MaxUint16 {
		return nil
	}

	layer4 := newLayer4(key.GetProto(), uint32(destPort))
	if layer4 == nil {
		return nil
	}

	startTime := flow.GetStartTime()
	if startTime <= 0 {
		return nil
	}

	calicoFlow := &pb.CalicoFlow{
		Time:             timestamppb.New(time.Unix(startTime, 0)),
		TrafficDirection: convertReporter(key.GetReporter()),
		Verdict:          convertAction(key.GetAction()),
		SourceIps:        sourceIPs,
		DestinationIps:   destinationIPs,
		Layer4:           layer4,
		SourceEndpoint: &pb.CalicoEndpoint{
			Namespace:      key.GetSourceNamespace(),
			Labels:         flow.GetSourceLabels(),
			AggregatedName: key.GetSourceName(),
		},
		DestinationEndpoint: &pb.CalicoEndpoint{
			Namespace:      key.GetDestNamespace(),
			Labels:         flow.GetDestLabels(),
			AggregatedName: key.GetDestName(),
		},
	}

	if key.GetDestServiceName() != "" {
		calicoFlow.DestinationService = &pb.Service{
			Name:      key.GetDestServiceName(),
			Namespace: key.GetDestServiceNamespace(),
		}
	}

	setPolicies(calicoFlow, key.GetPolicies().GetEnforcedPolicies())

	return calicoFlow
}

// convertReporter maps the Goldmane reporter to the direction whose policy was evaluated:
// the source endpoint's node evaluates egress policy, the destination's node ingress policy.
func convertReporter(reporter goldmanepb.Reporter) pb.TrafficDirection {
	switch reporter {
	case goldmanepb.Reporter_Src:
		return pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS
	case goldmanepb.Reporter_Dst:
		return pb.TrafficDirection_TRAFFIC_DIRECTION_INGRESS
	case goldmanepb.Reporter_ReporterUnspecified:
		return pb.TrafficDirection_TRAFFIC_DIRECTION_TRAFFIC_DIRECTION_UNKNOWN_UNSPECIFIED
	default:
		return pb.TrafficDirection_TRAFFIC_DIRECTION_TRAFFIC_DIRECTION_UNKNOWN_UNSPECIFIED
	}
}

// convertAction maps the flow's final Goldmane action to a verdict.
func convertAction(action goldmanepb.Action) pb.Verdict {
	switch action {
	case goldmanepb.Action_Allow:
		return pb.Verdict_VERDICT_FORWARDED
	case goldmanepb.Action_Deny:
		return pb.Verdict_VERDICT_DROPPED
	case goldmanepb.Action_ActionUnspecified, goldmanepb.Action_Pass:
		// Pass only defers to the next tier; it is never a flow's final action.
		return pb.Verdict_VERDICT_UNKNOWN_UNSPECIFIED
	default:
		return pb.Verdict_VERDICT_UNKNOWN_UNSPECIFIED
	}
}

// setPolicies fills the flow's allowed/denied-by policies for its direction from the
// enforced policy trace. Pass hits only defer to the next tier, so they are skipped.
// An end-of-tier hit (the default deny when a tier's policies select the endpoint but
// none matched) is attributed to the policy that triggered it.
func setPolicies(flow *pb.CalicoFlow, hits []*goldmanepb.PolicyHit) {
	for _, hit := range hits {
		policy := hit
		if hit.GetKind() == goldmanepb.PolicyKind_EndOfTier && hit.GetTrigger() != nil {
			policy = hit.GetTrigger()
		}

		p := &pb.Policy{
			Name:      policy.GetName(),
			Namespace: policy.GetNamespace(),
			Kind:      policy.GetKind().String(),
		}

		switch {
		case hit.GetAction() == goldmanepb.Action_Allow && flow.GetTrafficDirection() == pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS:
			flow.EgressAllowedBy = append(flow.EgressAllowedBy, p)
		case hit.GetAction() == goldmanepb.Action_Allow && flow.GetTrafficDirection() == pb.TrafficDirection_TRAFFIC_DIRECTION_INGRESS:
			flow.IngressAllowedBy = append(flow.IngressAllowedBy, p)
		case hit.GetAction() == goldmanepb.Action_Deny && flow.GetTrafficDirection() == pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS:
			flow.EgressDeniedBy = append(flow.EgressDeniedBy, p)
		case hit.GetAction() == goldmanepb.Action_Deny && flow.GetTrafficDirection() == pb.TrafficDirection_TRAFFIC_DIRECTION_INGRESS:
			flow.IngressDeniedBy = append(flow.IngressDeniedBy, p)
		}
	}
}

// normalizeIPs parses IP address strings and returns them in canonical form, skipping
// invalid ones and unmapping IPv4-mapped IPv6 addresses.
func normalizeIPs(ips []string) []string {
	normalized := make([]string, 0, len(ips))

	for _, ip := range ips {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			continue
		}

		normalized = append(normalized, addr.Unmap().String())
	}

	return normalized
}

// newLayer4 builds the Layer4 message for a Goldmane protocol name. Returns nil for
// protocols that a flow's Layer4 cannot represent (e.g. ipip, esp).
func newLayer4(proto string, destPort uint32) *pb.Layer4 {
	switch proto {
	case goldmaneProtoTCP:
		return &pb.Layer4{Protocol: &pb.Layer4_Tcp{Tcp: &pb.TCP{DestinationPort: destPort, Flags: &pb.TCPFlags{}}}}
	case goldmaneProtoUDP:
		return &pb.Layer4{Protocol: &pb.Layer4_Udp{Udp: &pb.UDP{DestinationPort: destPort}}}
	case goldmaneProtoSCTP:
		return &pb.Layer4{Protocol: &pb.Layer4_Sctp{Sctp: &pb.SCTP{DestinationPort: destPort}}}
	case goldmaneProtoICMP:
		return &pb.Layer4{Protocol: &pb.Layer4_Icmpv4{Icmpv4: &pb.ICMPv4{}}}
	case goldmaneProtoICMP6:
		return &pb.Layer4{Protocol: &pb.Layer4_Icmpv6{Icmpv6: &pb.ICMPv6{}}}
	default:
		return nil
	}
}
