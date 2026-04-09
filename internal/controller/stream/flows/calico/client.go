// Copyright 2026 Illumio, Inc. All Rights Reserved.

package calico

import (
	"context"
	"fmt"
	"strconv"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	goldmanepb "github.com/illumio/cloud-operator/api/illumio/cloud/goldmane/v1"
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/goldmane"
)

// TODO: Calico Goldmane flows currently use workload identifiers (name/namespace/type) instead of
// IP addresses like Cilium, Falco, and OVN-K five-tuple flows. Once upstream issue
// projectcalico/calico#10601 is resolved, update to include source/dest IP addresses
// for consistency with other flow collectors.

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

// Run connects to Goldmane, streams flows from it, and sends them to the flow sink.
// The connection is closed when Run returns, so each retry dials a fresh connection.
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

	stream, err := goldmanepb.NewFlowsClient(conn).Stream(ctx, &goldmanepb.StreamRequest{})
	if err != nil {
		c.logger.Error("Error starting Goldmane flow stream", zap.Error(err))

		return err
	}

	for {
		resp, err := stream.Recv()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			c.logger.Warn("Failed to receive flow from Goldmane stream", zap.Error(err))

			return err
		}

		flow := convertGoldmaneFlow(resp)
		if flow == nil {
			continue
		}

		c.flowSink.IncrementFlowsReceived()

		err = c.flowSink.CacheFlow(ctx, flow)
		if err != nil {
			c.logger.Error("Failed to cache Calico flow", zap.Error(err))

			return err
		}
	}
}

// convertGoldmaneFlow converts a Goldmane StreamResponse to a CalicoFlow.
func convertGoldmaneFlow(resp *goldmanepb.StreamResponse) *pb.CalicoFlow {
	if resp == nil || resp.GetFlow() == nil || resp.GetFlow().GetKey() == nil {
		return nil
	}

	flow := resp.GetFlow()
	key := flow.GetKey()

	// Parse timestamps (Goldmane returns Unix timestamps as strings)
	startTime := parseUnixTimestamp(flow.GetStartTime())
	endTime := parseUnixTimestamp(flow.GetEndTime())

	// Parse destination port
	destPort := parseUint32(key.GetDestPort())
	destServicePort := parseUint32(key.GetDestServicePort())

	// Parse statistics
	packetsIn := parseUint64(flow.GetPacketsIn())
	packetsOut := parseUint64(flow.GetPacketsOut())
	bytesIn := parseUint64(flow.GetBytesIn())
	bytesOut := parseUint64(flow.GetBytesOut())
	numConnectionsStarted := parseUint64(flow.GetNumConnectionsStarted())
	numConnectionsCompleted := parseUint64(flow.GetNumConnectionsCompleted())
	numConnectionsLive := parseUint64(flow.GetNumConnectionsLive())

	// Convert policies
	var policies *pb.CalicoPolicies
	if key.GetPolicies() != nil {
		policies = convertGoldmanePolicies(key.GetPolicies())
	}

	calicoFlow := &pb.CalicoFlow{
		StartTime:               startTime,
		EndTime:                 endTime,
		SourceName:              key.GetSourceName(),
		SourceNamespace:         key.GetSourceNamespace(),
		SourceType:              key.GetSourceType(),
		DestName:                key.GetDestName(),
		DestNamespace:           key.GetDestNamespace(),
		DestType:                key.GetDestType(),
		DestPort:                destPort,
		DestServiceName:         key.GetDestServiceName(),
		DestServiceNamespace:    key.GetDestServiceNamespace(),
		DestServicePortName:     key.GetDestServicePortName(),
		DestServicePort:         destServicePort,
		Proto:                   key.GetProto(),
		Reporter:                key.GetReporter(),
		Action:                  key.GetAction(),
		SourceLabels:            flow.GetSourceLabels(),
		DestLabels:              flow.GetDestLabels(),
		PacketsIn:               packetsIn,
		PacketsOut:              packetsOut,
		BytesIn:                 bytesIn,
		BytesOut:                bytesOut,
		NumConnectionsStarted:   numConnectionsStarted,
		NumConnectionsCompleted: numConnectionsCompleted,
		NumConnectionsLive:      numConnectionsLive,
		Policies:                policies,
	}

	return calicoFlow
}

// convertGoldmanePolicies converts Goldmane Policies to CalicoPolicies.
func convertGoldmanePolicies(policies *goldmanepb.Policies) *pb.CalicoPolicies {
	if policies == nil {
		return nil
	}

	enforcedPolicies := make([]*pb.CalicoPolicy, 0, len(policies.GetEnforcedPolicies()))
	for _, p := range policies.GetEnforcedPolicies() {
		enforcedPolicies = append(enforcedPolicies, &pb.CalicoPolicy{
			Kind:      p.GetKind(),
			Namespace: p.GetNamespace(),
			Name:      p.GetName(),
			Tier:      p.GetTier(),
			Action:    p.GetAction(),
		})
	}

	pendingPolicies := make([]*pb.CalicoPolicy, 0, len(policies.GetPendingPolicies()))
	for _, p := range policies.GetPendingPolicies() {
		pendingPolicies = append(pendingPolicies, &pb.CalicoPolicy{
			Kind:      p.GetKind(),
			Namespace: p.GetNamespace(),
			Name:      p.GetName(),
			Tier:      p.GetTier(),
			Action:    p.GetAction(),
		})
	}

	return &pb.CalicoPolicies{
		EnforcedPolicies: enforcedPolicies,
		PendingPolicies:  pendingPolicies,
	}
}

// parseUnixTimestamp parses a Unix timestamp string to a protobuf Timestamp.
func parseUnixTimestamp(s string) *timestamppb.Timestamp {
	if s == "" {
		return nil
	}

	seconds, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}

	return &timestamppb.Timestamp{Seconds: seconds}
}

// parseUint32 parses a string to uint32.
func parseUint32(s string) uint32 {
	if s == "" {
		return 0
	}

	val, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0
	}

	return uint32(val)
}

// parseUint64 parses a string to uint64.
func parseUint64(s string) uint64 {
	if s == "" {
		return 0
	}

	val, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}

	return val
}
