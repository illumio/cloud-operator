package controller

import (
	"context"
	"errors"
	"fmt"

	"github.com/cilium/cilium/api/v1/flow"
	observer "github.com/cilium/cilium/api/v1/observer"
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8scluster/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Collector collects flows from Cilium Hubble Relay running in this cluster.
type Collector struct {
	logger *zap.SugaredLogger
	client observer.ObserverClient
}

// discoverHubbleRelayAddress uses a kubernetes clientset in order to discover the address of hubble-relay within kube-system.
func discoverHubbleRelayAddress(ctx context.Context, logger *zap.SugaredLogger, ciliumNamespace string, clientset kubernetes.Interface) (string, error) {
	service, err := clientset.CoreV1().Services("kube-system").Get(ctx, ciliumNamespace, metav1.GetOptions{})
	if err != nil {
		return "", errors.New("hubble Relay service not found; disabling Cilium flow collection")
	}

	if len(service.Spec.Ports) == 0 {
		return "", errors.New("hubble Relay service has no ports; disabling Cilium flow collection")
	}

	address := fmt.Sprintf("%s:%d", service.Spec.ClusterIP, service.Spec.Ports[0].Port)
	return address, nil
}

// NewCollector connects to Hubble Relay, sets up an Observer client, and returns a new Collector using it.
func NewCollector(ctx context.Context, logger *zap.SugaredLogger, ciliumNamespace string) (*Collector, error) {
	config, err := NewClientSet()
	if err != nil {
		return nil, fmt.Errorf("failed to create new client set: %w", err)
	}
	hubbleAddress, err := discoverHubbleRelayAddress(ctx, logger, ciliumNamespace, config)
	if err != nil {
		return nil, err
	}
	// Adjust this address if needed
	conn, err := grpc.NewClient(hubbleAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Cilium Hubble Relay: %w", err)
	}
	hubbleClient := observer.NewObserverClient(conn)
	return &Collector{logger: logger, client: hubbleClient}, nil
}

// collectFlows continuously collects flows and sends them to CloudSecure.
func (fm *Collector) collectFlows(ctx context.Context, sm streamManager) error {
	// Fetch network flows
	for {
		// Check if context is canceled
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		flows, err := fm.readFlows(ctx)
		if err != nil {
			fm.logger.Errorw("Error fetching network flows", "error", err)
			return err
		}

		// Process and store flows
		for _, flow := range flows {
			flowObj := flow.GetFlow()
			ciliumFlow := pb.CiliumFlow{
				Time:             flowObj.GetTime(),
				NodeName:         flowObj.GetNodeName(),
				Verdict:          pb.Verdict(flowObj.GetVerdict()),
				TrafficDirection: pb.TrafficDirection(flowObj.GetTrafficDirection()),
				Layer3: &pb.IP{
					Source:      flowObj.GetIP().GetSource(),
					Destination: flowObj.GetIP().GetDestination(),
					IpVersion:   pb.IPVersion(flowObj.GetIP().GetIpVersion()),
				},
				Layer4: convertLayer4(flowObj.GetL4()),
				EventType: &pb.CiliumEventType{
					Type:    flowObj.GetEventType().GetType(),
					SubType: flowObj.GetEventType().GetSubType(),
				},
				SourceEndpoint: &pb.Endpoint{
					Id:          flowObj.GetSource().GetID(),
					Identity:    flowObj.GetSource().GetIdentity(),
					ClusterName: flowObj.GetSource().GetClusterName(),
					Namespace:   flowObj.GetSource().GetNamespace(),
					Labels:      flowObj.GetSource().GetLabels(),
					PodName:     flowObj.GetSource().GetPodName(),
					Workloads:   convertCiliumWorkflows(flowObj.GetSource().GetWorkloads()),
				},
				DestinationEndpoint: &pb.Endpoint{
					Id:          flowObj.GetDestination().GetID(),
					Identity:    flowObj.GetDestination().GetIdentity(),
					ClusterName: flowObj.GetDestination().GetClusterName(),
					Namespace:   flowObj.GetDestination().GetNamespace(),
					Labels:      flowObj.GetDestination().GetLabels(),
					PodName:     flowObj.GetDestination().GetPodName(),
					Workloads:   convertCiliumWorkflows(flowObj.GetDestination().GetWorkloads()),
				},
				Interface: &pb.NetworkInterface{
					Index: flowObj.GetInterface().GetIndex(),
					Name:  flowObj.GetInterface().GetName(),
				},
				ProxyPort:        int32(flowObj.GetProxyPort()),
				EgressAllowedBy:  convertCiliumPolicies(flowObj.GetEgressAllowedBy()),
				IngressAllowedBy: convertCiliumPolicies(flowObj.GetIngressAllowedBy()),
				EgressDeniedBy:   convertCiliumPolicies(flowObj.GetEgressDeniedBy()),
				IngressDeniedBy:  convertCiliumPolicies(flowObj.GetIngressDeniedBy()),
			}

			err = sendNetworkFlowsData(&sm, &ciliumFlow)
			if err != nil {
				fm.logger.Errorw("Cannot send object metadata", "error", err)
				return err
			}
		}
	}
}

// convertLayer4 function converts a slice of flow.Layer4 objects to a slice of pb.Layer4 objects.
func convertLayer4(l4 *flow.Layer4) *pb.Layer4 {
	layer4 := &pb.Layer4{}
	if l4 == nil {
		return layer4
	}
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

// convertCiliumWorkflows function converts a slice of flow.Workload objects to a slice of pb.Workload objects.
func convertCiliumWorkflows(workloads []*flow.Workload) []*pb.Workload {
	protoWorkloads := []*pb.Workload{}
	for _, workload := range workloads {
		protoWorkload := &pb.Workload{
			Name: workload.GetName(),
			Kind: workload.GetKind(),
		}
		protoWorkloads = append(protoWorkloads, protoWorkload)
	}
	return protoWorkloads
}

// convertCiliumPolicies function converts a slice of flow.Policy objects to a slice of pb.Policy objects.
func convertCiliumPolicies(policies []*flow.Policy) []*pb.Policy {
	protoPolicies := []*pb.Policy{}
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

// readFlows uses the observerClient to make gRPC calls to hubble-relay and grab the last x amount of flows.
func (fm *Collector) readFlows(ctx context.Context) ([]*observer.GetFlowsResponse, error) {
	req := &observer.GetFlowsRequest{
		Number: 10,
	}
	observerClient := fm.client
	stream, err := observerClient.GetFlows(ctx, req)
	if err != nil {
		fm.logger.Errorw("Error getting network flows", "error", err)
		return []*observer.GetFlowsResponse{}, err
	}

	var flows []*observer.GetFlowsResponse
	for {
		flow, err := stream.Recv()
		if err != nil {
			break
		}
		flows = append(flows, flow)
	}

	return flows, nil
}
