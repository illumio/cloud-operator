// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"fmt"

	"github.com/cilium/cilium/api/v1/flow"
	observer "github.com/cilium/cilium/api/v1/observer"
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/hubble"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
	"go.uber.org/zap"
)

// CiliumFlowCollector collects flows from Cilium Hubble Relay running in this cluster.
type CiliumFlowCollector struct {
	logger *zap.Logger
	client observer.ObserverClient
}

const (
	ciliumHubbleRelayMaxFlowCount uint64 = 100

	// Constants for fetching the mTLS secret
	ciliumHubbleMTLSSecretName string = "hubble-relay-client-certs"
	ciliumHubbleRelayNamespace string = "kube-system"
)

// newCiliumFlowCollector connects to Cilium Hubble Relay, sets up an Observer client, and returns a new Collector using it.
func newCiliumFlowCollector(ctx context.Context, logger *zap.Logger, ciliumNamespace string, tlsAuthProperties tls.AuthProperties) (*CiliumFlowCollector, error) {
	clientset, err := NewClientSet()
	if err != nil {
		return nil, fmt.Errorf("failed to create new client set: %w", err)
	}

	service, err := hubble.DiscoverCiliumHubbleRelay(ctx, ciliumNamespace, clientset)
	if err != nil {
		return nil, fmt.Errorf("failed to discover Cilium Hubble Relay service: %w", err)
	}

	hubbleAddress, err := hubble.GetAddressFromService(service)
	if err != nil {
		return nil, fmt.Errorf("failed to discover Cilium Hubble Relay address: %w", err)
	}
	tlsConfig, err := hubble.GetTLSConfig(ctx, clientset, logger, ciliumHubbleMTLSSecretName, ciliumHubbleRelayNamespace)
	if err != nil || tlsAuthProperties.DisableTLS {
		tlsConfig = nil
	}

	conn, err := hubble.ConnectToHubbleRelay(ctx, logger, hubbleAddress, tlsConfig, tlsAuthProperties.DisableALPN)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Cilium Hubble Relay: %w", err)
	}
	logger.Info("Successfully connected to Cilium Hubble Relay", zap.String("address", hubbleAddress))

	hubbleClient := observer.NewObserverClient(conn)
	return &CiliumFlowCollector{logger: logger, client: hubbleClient}, nil
}

// convertCiliumIP converts a flow.IP object to a pb.IP object.
func convertCiliumIP(IP *flow.IP) *pb.IP {
	if IP == nil {
		return nil
	}
	return &pb.IP{
		Source:      IP.GetSource(),
		Destination: IP.GetDestination(),
		IpVersion:   pb.IPVersion(IP.GetIpVersion()),
	}
}

// convertCiliumLayer4 function converts a slice of flow.Layer4 objects to a slice of pb.Layer4 objects.
func convertCiliumLayer4(l4 *flow.Layer4) *pb.Layer4 {
	if l4 == nil {
		return nil
	}
	layer4 := &pb.Layer4{}
	switch protocol := l4.Protocol.(type) {
	case *flow.Layer4_TCP:
		layer4.Protocol = &pb.Layer4_Tcp{
			Tcp: &pb.TCP{
				SourcePort:      protocol.TCP.SourcePort,
				DestinationPort: protocol.TCP.DestinationPort,
			},
		}
	case *flow.Layer4_UDP:
		layer4.Protocol = &pb.Layer4_Udp{
			Udp: &pb.UDP{
				SourcePort:      protocol.UDP.SourcePort,
				DestinationPort: protocol.UDP.DestinationPort,
			},
		}
	case *flow.Layer4_ICMPv4:
		layer4.Protocol = &pb.Layer4_Icmpv4{
			Icmpv4: &pb.ICMPv4{
				Type: protocol.ICMPv4.Type,
				Code: protocol.ICMPv4.Code,
			},
		}
	case *flow.Layer4_ICMPv6:
		layer4.Protocol = &pb.Layer4_Icmpv6{
			Icmpv6: &pb.ICMPv6{
				Type: protocol.ICMPv6.Type,
				Code: protocol.ICMPv6.Code,
			},
		}
	case *flow.Layer4_SCTP:
		layer4.Protocol = &pb.Layer4_Sctp{
			Sctp: &pb.SCTP{
				SourcePort:      protocol.SCTP.SourcePort,
				DestinationPort: protocol.SCTP.DestinationPort,
			},
		}
	default:
	}

	return layer4
}

// convertCiliumWorkflows converts a slice of flow.Workload objects to a slice of pb.Workload objects.
func convertCiliumWorkflows(workloads []*flow.Workload) []*pb.Workload {
	protoWorkloads := make([]*pb.Workload, 0, len(workloads))
	for _, workload := range workloads {
		protoWorkload := &pb.Workload{
			Name: workload.GetName(),
			Kind: workload.GetKind(),
		}
		protoWorkloads = append(protoWorkloads, protoWorkload)
	}
	return protoWorkloads
}

// convertCiliumPolicies converts a slice of flow.Policy objects to a slice of pb.Policy objects.
func convertCiliumPolicies(policies []*flow.Policy) []*pb.Policy {
	protoPolicies := make([]*pb.Policy, 0, len(policies))
	for _, policy := range policies {
		protoPolicy := &pb.Policy{
			Name:      policy.GetName(),
			Namespace: policy.GetNamespace(),
			Labels:    policy.GetLabels(),
			Revision:  policy.GetRevision(),
		}
		protoPolicies = append(protoPolicies, protoPolicy)
	}
	return protoPolicies
}

// exportCiliumFlows makes one stream gRPC call to hubble-relay to collect, convert, and export flows into the given stream.
func (fm *CiliumFlowCollector) exportCiliumFlows(ctx context.Context, sm *streamManager) error {
	req := &observer.GetFlowsRequest{
		Number: ciliumHubbleRelayMaxFlowCount,
		Follow: true,
	}
	observerClient := fm.client
	stream, err := observerClient.GetFlows(ctx, req)
	if err != nil {
		err = tls.AsTLSHandshakeError(err)
		fm.logger.Error("Error getting network flows", zap.Error(err))
		return err
	}
	defer func() {
		err = stream.CloseSend()
		if err != nil {
			fm.logger.Error("Error closing observerClient stream", zap.Error(err))
		}
	}()
	for {
		select {
		case <-ctx.Done():
			fm.logger.Warn("Context cancelled, stopping flow export")
			return ctx.Err()
		default:
		}
		flow, err := stream.Recv()
		if err != nil {
			fm.logger.Warn("Failed to get flow log from stream", zap.Error(err))
			return err
		}
		ciliumFlow := convertCiliumFlow(flow)
		if ciliumFlow == nil {
			continue
		}
		err = sm.FlowCache.CacheFlow(ctx, ciliumFlow)
		if err != nil {
			fm.logger.Error("Failed to cache flow", zap.Error(err))
			return err
		}
	}
}

// convertCiliumFlow converts a GetFlowsResponse object to a CiliumFlow object
func convertCiliumFlow(flow *observer.GetFlowsResponse) *pb.CiliumFlow {
	flowObj := flow.GetFlow()
	// Check for nil fields
	if flowObj.GetTime() == nil ||
		flowObj.GetNodeName() == "" ||
		flowObj.GetTrafficDirection().String() == "" ||
		flowObj.GetVerdict().String() == "" ||
		flowObj.GetIP() == nil ||
		flowObj.GetL4() == nil {
		// Return nil if any of the essential fields are nil
		return nil
	}

	ciliumFlow := &pb.CiliumFlow{
		Time:               flowObj.GetTime(),
		NodeName:           flowObj.GetNodeName(),
		Verdict:            pb.Verdict(flowObj.GetVerdict()),
		TrafficDirection:   pb.TrafficDirection(flowObj.GetTrafficDirection()),
		Layer3:             convertCiliumIP(flowObj.GetIP()),
		Layer4:             convertCiliumLayer4(flowObj.GetL4()),
		DestinationService: &pb.Service{Name: flowObj.GetDestinationService().GetName(), Namespace: flowObj.GetDestinationService().GetNamespace()},
		EgressAllowedBy:    convertCiliumPolicies(flowObj.GetEgressAllowedBy()),
		IngressAllowedBy:   convertCiliumPolicies(flowObj.GetIngressAllowedBy()),
		EgressDeniedBy:     convertCiliumPolicies(flowObj.GetEgressDeniedBy()),
		IngressDeniedBy:    convertCiliumPolicies(flowObj.GetIngressDeniedBy()),
		IsReply:            flowObj.GetIsReply(),
	}
	if flowObj.GetSource() != nil {
		ciliumFlow.SourceEndpoint = &pb.Endpoint{
			Uid:         flowObj.GetSource().GetID(),
			ClusterName: flowObj.GetSource().GetClusterName(),
			Namespace:   flowObj.GetSource().GetNamespace(),
			Labels:      flowObj.GetSource().GetLabels(),
			PodName:     flowObj.GetSource().GetPodName(),
			Workloads:   convertCiliumWorkflows(flowObj.GetSource().GetWorkloads()),
		}
	}
	if flowObj.GetDestination() != nil {
		ciliumFlow.DestinationEndpoint = &pb.Endpoint{
			Uid:         flowObj.GetDestination().GetID(),
			ClusterName: flowObj.GetDestination().GetClusterName(),
			Namespace:   flowObj.GetDestination().GetNamespace(),
			Labels:      flowObj.GetDestination().GetLabels(),
			PodName:     flowObj.GetDestination().GetPodName(),
			Workloads:   convertCiliumWorkflows(flowObj.GetDestination().GetWorkloads()),
		}
	}
	return ciliumFlow
}
