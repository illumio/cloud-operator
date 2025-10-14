// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	cryptotls "crypto/tls"
	"fmt"
	"sync"

	"github.com/cilium/cilium/api/v1/flow"
	observer "github.com/cilium/cilium/api/v1/observer"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/hubble"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

// CiliumFlowCollector collects flows from Cilium Hubble Relay running in this cluster.
type CiliumFlowCollector struct {
	logger       *zap.Logger
	client       observer.ObserverClient
	clientset    *kubernetes.Clientset
	podNodeCache map[string]string
	pncMutex     sync.RWMutex
}

const (
	ciliumHubbleRelayMaxFlowCount uint64 = 100

	// Constants for fetching the mTLS secret.
	ciliumHubbleMTLSSecretName string = "hubble-relay-client-certs" //nolint:gosec
	ciliumHubbleRelayNamespace string = "kube-system"
)

// newCiliumFlowCollector connects to Cilium Hubble Relay, sets up an Observer client,
// and returns a new Collector using it. It tries namespaces until discovery succeeds.
func newCiliumFlowCollector(ctx context.Context, logger *zap.Logger, ciliumNamespaces []string, tlsAuthProperties tls.AuthProperties) (*CiliumFlowCollector, error) {
	clientset, err := NewClientSet()
	if err != nil {
		return nil, fmt.Errorf("failed to create new client set: %w", err)
	}

	var hubbleAddress string

	var tlsConfig *cryptotls.Config

	var discoveryErr error

	for _, ciliumNamespace := range ciliumNamespaces {
		// Step 1: Try to discover Hubble Relay
		service, err := hubble.DiscoverCiliumHubbleRelay(ctx, ciliumNamespace, clientset)
		if err != nil {
			logger.Debug("Failed to discover Cilium Hubble Relay service",
				zap.String("namespace", ciliumNamespace), zap.Error(err))
			discoveryErr = err

			continue
		}

		// Step 2: Get Relay address
		hubbleAddress, err = hubble.GetAddressFromService(service)
		if err != nil {
			return nil, fmt.Errorf("failed to discover Cilium Hubble Relay address: %w", err)
		}

		// Step 3: Get TLS config (unless disabled)
		tlsConfig, err = hubble.GetTLSConfig(ctx, clientset, logger, ciliumHubbleMTLSSecretName, ciliumNamespace)
		if err != nil {
			logger.Warn("Failed to get TLS config", zap.String("namespace", ciliumNamespace), zap.Error(err))

			tlsConfig = nil
		}

		if tlsAuthProperties.DisableTLS {
			logger.Info("TLS is disabled via configuration")

			tlsConfig = nil
		}

		// Found a working namespace — stop checking
		logger.Info("Cilium Hubble Relay discovered successfully",
			zap.String("namespace", ciliumNamespace),
			zap.String("address", hubbleAddress))

		break
	}

	if hubbleAddress == "" {
		return nil, fmt.Errorf("failed to discover Cilium Hubble Relay: %w", discoveryErr)
	}

	// Step 4: Connect to Relay
	conn, err := hubble.ConnectToHubbleRelay(ctx, logger, hubbleAddress, tlsConfig, tlsAuthProperties.DisableALPN)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Cilium Hubble Relay: %w", err)
	}

	logger.Info("Successfully connected to Cilium Hubble Relay",
		zap.String("address", hubbleAddress))

 hubbleClient := observer.NewObserverClient(conn)

	collector := &CiliumFlowCollector{
		logger:       logger,
		client:       hubbleClient,
		clientset:    clientset,
		podNodeCache: make(map[string]string),
	}

	return collector, nil
}

// resolveNodeName attempts to resolve a PodName@namespace to nodeName using the k8s API.
// Caches results in podNodeCache to avoid repeated API calls.
func (fm *CiliumFlowCollector) resolveNodeName(ctx context.Context, podName, namespace string) (string, error) {
	if podName == "" || namespace == "" || fm.clientset == nil {
		return "", nil
	}

	key := namespace + "/" + podName

	fm.pncMutex.RLock()
	if n, ok := fm.podNodeCache[key]; ok {
		fm.pncMutex.RUnlock()
		return n, nil
	}
	fm.pncMutex.RUnlock()

	pod, err := fm.clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		// don't log loudly here; caller will decide
		return "", err
	}
	node := pod.Spec.NodeName
	if node == "" {
		return "", nil
	}

	fm.pncMutex.Lock()
	fm.podNodeCache[key] = node
	fm.pncMutex.Unlock()

	return node, nil
}

// convertCiliumIP converts a flow.IP object to a pb.IP object.
func convertCiliumIP(ipAddress *flow.IP) *pb.IP {
	if ipAddress == nil {
		return nil
	}

	return &pb.IP{
		Source:      ipAddress.GetSource(),
		Destination: ipAddress.GetDestination(),
		IpVersion:   pb.IPVersion(ipAddress.GetIpVersion()),
	}
}

// convertCiliumLayer4 function converts a slice of flow.Layer4 objects to a slice of pb.Layer4 objects.
func convertCiliumLayer4(l4 *flow.Layer4) *pb.Layer4 {
	if l4 == nil {
		return nil
	}

	layer4 := &pb.Layer4{}

	switch protocol := l4.GetProtocol().(type) {
	case *flow.Layer4_TCP:
		layer4.Protocol = &pb.Layer4_Tcp{
			Tcp: &pb.TCP{
				SourcePort:      protocol.TCP.GetSourcePort(),
				DestinationPort: protocol.TCP.GetDestinationPort(),
			},
		}
	case *flow.Layer4_UDP:
		layer4.Protocol = &pb.Layer4_Udp{
			Udp: &pb.UDP{
				SourcePort:      protocol.UDP.GetSourcePort(),
				DestinationPort: protocol.UDP.GetDestinationPort(),
			},
		}
	case *flow.Layer4_ICMPv4:
		layer4.Protocol = &pb.Layer4_Icmpv4{
			Icmpv4: &pb.ICMPv4{
				Type: protocol.ICMPv4.GetType(),
				Code: protocol.ICMPv4.GetCode(),
			},
		}
	case *flow.Layer4_ICMPv6:
		layer4.Protocol = &pb.Layer4_Icmpv6{
			Icmpv6: &pb.ICMPv6{
				Type: protocol.ICMPv6.GetType(),
				Code: protocol.ICMPv6.GetCode(),
			},
		}
	case *flow.Layer4_SCTP:
		layer4.Protocol = &pb.Layer4_Sctp{
			Sctp: &pb.SCTP{
				SourcePort:      protocol.SCTP.GetSourcePort(),
				DestinationPort: protocol.SCTP.GetDestinationPort(),
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
			Kind:      policy.GetKind(),
		}
		protoPolicies = append(protoPolicies, protoPolicy)
	}

	return protoPolicies
}

// exportCiliumFlows makes one stream gRPC call to hubble-relay to collect, convert, and export flows into the given stream.
func (fm *CiliumFlowCollector) convertCiliumFlow(ctx context.Context, flowResp *observer.GetFlowsResponse) *pb.CiliumFlow {
	flowObj := flowResp.GetFlow()
	// Only require minimal fields needed for timestamp and IP-based keying.
	if flowObj == nil || flowObj.GetTime() == nil || flowObj.GetIP() == nil {
		// Log diagnostic details
		fm.logger.Debug("Dropping Cilium flow: missing essential fields",
			zap.Any("flow", flowObj),
			zap.Bool("has_time", flowObj != nil && flowObj.GetTime() != nil),
			zap.Bool("has_ip", flowObj != nil && flowObj.GetIP() != nil),
		)
		return nil
	}

	// Attempt best-effort resolution for nodeName if empty
	nodeName := flowObj.GetNodeName()
	if nodeName == "" {
		if flowObj.GetSource() != nil {
			pod := flowObj.GetSource().GetPodName()
			ns := flowObj.GetSource().GetNamespace()
			if pod != "" && ns != "" {
				if resolved, err := fm.resolveNodeName(ctx, pod, ns); err == nil && resolved != "" {
					nodeName = resolved
				} else if err != nil {
					// Debug only — not fatal
					fm.logger.Debug("Could not resolve nodeName from k8s API", zap.String("pod", pod), zap.String("namespace", ns), zap.Error(err))
				}
			}
		}
	}

	ciliumFlow := &pb.CiliumFlow{
		Time:               flowObj.GetTime(),
		NodeName:           nodeName,
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

	// If NodeName is still empty, emit a warning but accept flow so it reaches backend.
	if ciliumFlow.NodeName == "" {
		fm.logger.Warn("Cilium flow has no nodeName after best-effort resolution; accepting flow but mapping may be incomplete", zap.Any("flow", flowObj))
	}

	return ciliumFlow
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

		flowResp, err := stream.Recv()
		if err != nil {
			fm.logger.Warn("Failed to get flow log from stream", zap.Error(err))

			return err
		}

		ciliumFlow := fm.convertCiliumFlow(ctx, flowResp)
		if ciliumFlow == nil {
			fo := flowResp.GetFlow()
			hasTime := fo != nil && fo.GetTime() != nil
			hasIP := fo != nil && fo.GetIP() != nil
			hasL4 := fo != nil && fo.GetL4() != nil
			var srcNs, dstNs, node, verdict, direction string
			if fo != nil {
				node = fo.GetNodeName()
				verdict = fo.GetVerdict().String()
				direction = fo.GetTrafficDirection().String()
				if fo.GetSource() != nil { srcNs = fo.GetSource().GetNamespace() }
				if fo.GetDestination() != nil { dstNs = fo.GetDestination().GetNamespace() }
			}
			fm.logger.Debug("Skipping Cilium flow due to missing fields",
				zap.Bool("has_time", hasTime),
				zap.Bool("has_ip", hasIP),
				zap.Bool("has_l4", hasL4),
				zap.String("src_ns", srcNs),
				zap.String("dst_ns", dstNs),
				zap.String("node", node),
				zap.String("verdict", verdict),
				zap.String("direction", direction),
			)
			continue
		}

		// Debug: log the converted flow (summary). Warning: high volume in busy clusters.
		fm.logger.Debug("Converted Cilium flow (summary)",
			zap.Any("flow_summary", map[string]interface{}{
				"time":      ciliumFlow.GetTime(),
				"node":      ciliumFlow.GetNodeName(),
				"verdict":   ciliumFlow.GetVerdict().String(),
				"direction": ciliumFlow.GetTrafficDirection().String(),
				"src_ns": func() string {
					if ciliumFlow.GetSourceEndpoint() != nil {
						return ciliumFlow.GetSourceEndpoint().GetNamespace()
					}
					return ""
				}(),
				"src_pod": func() string {
					if ciliumFlow.GetSourceEndpoint() != nil {
						return ciliumFlow.GetSourceEndpoint().GetPodName()
					}
					return ""
				}(),
				"src_ip": func() string {
					if ciliumFlow.GetLayer3() != nil {
						return ciliumFlow.GetLayer3().GetSource()
					}
					return ""
				}(),
				"dst_ns": func() string {
					if ciliumFlow.GetDestinationEndpoint() != nil {
						return ciliumFlow.GetDestinationEndpoint().GetNamespace()
					}
					return ""
				}(),
				"dst_pod": func() string {
					if ciliumFlow.GetDestinationEndpoint() != nil {
						return ciliumFlow.GetDestinationEndpoint().GetPodName()
					}
					return ""
				}(),
				"dst_ip": func() string {
					if ciliumFlow.GetLayer3() != nil {
						return ciliumFlow.GetLayer3().GetDestination()
					}
					return ""
				}(),
			}),
		)

		err = sm.FlowCache.CacheFlow(ctx, ciliumFlow)
		if err != nil {
			fm.logger.Error("Failed to cache flow", zap.Error(err))

			return err
		}
	}
}

// convertCiliumFlow converts a GetFlowsResponse object to a CiliumFlow object.
func convertCiliumFlow(flow *observer.GetFlowsResponse) *pb.CiliumFlow {
	flowObj := flow.GetFlow()
	// Only require minimal fields needed for timestamp and IP-based keying.
	if flowObj == nil || flowObj.GetTime() == nil || flowObj.GetIP() == nil {
		return nil
	}

	ciliumFlow := &pb.CiliumFlow{
		Time:             flowObj.GetTime(),
		NodeName:         flowObj.GetNodeName(), // may be empty
		Verdict:          pb.Verdict(flowObj.GetVerdict()),
		TrafficDirection: pb.TrafficDirection(flowObj.GetTrafficDirection()),
		Layer3:           convertCiliumIP(flowObj.GetIP()),
		// Layer4 may be nil for some flow types (e.g., ICMP or L3-only). That's OK.
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
