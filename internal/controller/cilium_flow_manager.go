package controller

import (
	"context"
	"errors"
	"fmt"

	"github.com/cilium/cilium/api/v1/flow"
	observer "github.com/cilium/cilium/api/v1/observer"
	"github.com/go-logr/logr"
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8scluster/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// FlowManager holds a logger and Cilium client
type FlowManager struct {
	logger logr.Logger
	client observer.ObserverClient
}

// discoverHubbleRelayAddress uses a kubernetes clientset in order to discover the address of hubble-relay within kube-system.
func discoverHubbleRelayAddress(ctx context.Context, logger logr.Logger, clientset kubernetes.Interface) (string, error) {
	service, err := clientset.CoreV1().Services("kube-system").Get(ctx, "hubble-relay", metav1.GetOptions{})
	if err != nil {
		logger.Error(err, "Failed to get Hubble Relay service")
		return "", err
	}

	if len(service.Spec.Ports) == 0 {
		logger.Error(err, "Hubble Relay service has no ports")
		return "", errors.New("hubble relay service has no ports")
	}

	address := fmt.Sprintf("%s:%d", service.Spec.ClusterIP, service.Spec.Ports[0].Port)
	return address, nil
}

// initFlowManager connects to hubble, sets up a observerClient and creates a new FlowManager object.
func initFlowManager(ctx context.Context, logger logr.Logger) (FlowManager, error) {
	// Connect to Hubble
	config, err := NewClientSet()
	if err != nil {
		logger.Error(err, "Could not create a new client set")
		return FlowManager{}, err
	}
	hubbleAddress, err := discoverHubbleRelayAddress(ctx, logger, config)
	if err != nil {
		logger.Error(err, "Cannot find hubble-relay address")
		return FlowManager{}, err
	}
	// Adjust this address if needed
	conn, err := grpc.NewClient(hubbleAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		logger.Error(err, "Failed to connect to Hubble")
		return FlowManager{}, err
	}
	hubbleClient := observer.NewObserverClient(conn)
	return FlowManager{logger: logger, client: hubbleClient}, nil
}

// listenToFlows and fetches network flows in batches indefinitely.
func (fm *FlowManager) listenToFlows(ctx context.Context, sm streamManager) error {
	// Fetch network flows
	for {
		flows, err := fm.readFlows(ctx)
		if err != nil {
			fm.logger.Error(err, "Error fetching network flows")
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
				fm.logger.Error(err, "Cannot send object metadata")
				return err
			}
		}
	}
}

// convertLayer4 function converts a slice of flow.Layer4 objects to a slice of pb.Layer4 objects.
func convertLayer4(l4 *flow.Layer4) *pb.Layer4 {
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

// convertCiliumWorkflows function converts a slice of flow.Workload objects to a slice of pb.Workload objects.
func convertCiliumWorkflows(workloads []*flow.Workload) []*pb.Workload {
	var protoWorkloads []*pb.Workload
	for _, workload := range workloads {
		protoWorkload := &pb.Workload{
			Name: workload.GetName(),
			Kind: workload.GetKind(),
			// Add other fields as necessary
		}
		protoWorkloads = append(protoWorkloads, protoWorkload)
	}
	return protoWorkloads
}

// convertCiliumPolicies function converts a slice of flow.Policy objects to a slice of pb.Policy objects.
func convertCiliumPolicies(policies []*flow.Policy) []*pb.Policy {
	var protoPolicies []*pb.Policy

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
func (fm *FlowManager) readFlows(ctx context.Context) ([]*observer.GetFlowsResponse, error) {
	req := &observer.GetFlowsRequest{
		Number: 10,
	}
	observerClient := fm.client
	stream, err := observerClient.GetFlows(ctx, req)
	if err != nil {
		fm.logger.Error(err, "Error getting network flows")
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
