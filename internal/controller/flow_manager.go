package controller

import (
	"context"
	"errors"
	"fmt"
	"log"

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
		log.Fatalf("Failed to connect to Hubble: %v", err)
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
			sourceWorkloads := convertToProtoWorkloads(flowObj.GetSource().GetWorkloads())
			sourcePort := fm.getPortFromFlows(flowObj.GetL4(), true)
			source := pb.FlowMetadata{
				Ip:        flowObj.GetIP().GetSource(),
				Labels:    flowObj.GetSource().GetLabels(),
				Namespace: flowObj.GetSource().GetNamespace(),
				Name:      flowObj.GetNodeName(),
				Port:      int32(sourcePort),
				Protocol:  flowObj.GetL7().GetHttp().GetProtocol(),
				Workload:  sourceWorkloads,
			}
			destinationWorkloads := convertToProtoWorkloads(flowObj.GetDestination().GetWorkloads())
			destPort := fm.getPortFromFlows(flowObj.GetL4(), false)
			destination := pb.FlowMetadata{
				Ip:        flowObj.GetIP().GetDestination(),
				Labels:    flowObj.GetDestination().GetLabels(),
				Namespace: flowObj.GetDestination().GetNamespace(),
				Name:      flowObj.GetNodeName(),
				Port:      int32(destPort),
				Protocol:  flowObj.GetL7().GetHttp().GetProtocol(),
				Workload:  destinationWorkloads,
			}
			err = sendNetworkFlowsData(&sm, &source, &destination)
			if err != nil {
				fm.logger.Error(err, "Cannot send object metadata")
				return err
			}
		}
	}
}

// getPortFromFlows determines which port is being used in a given flow.
func (fm *FlowManager) getPortFromFlows(l4Object *flow.Layer4, isSourcePort bool) uint32 {
	if isSourcePort {
		if l4Object.GetTCP() != nil {
			return l4Object.GetTCP().GetSourcePort()
		} else if l4Object.GetSCTP() != nil {
			return l4Object.GetSCTP().GetSourcePort()
		} else if l4Object.GetUDP() != nil {
			return l4Object.GetUDP().GetSourcePort()
		}
	} else {
		if l4Object.GetTCP() != nil {
			return l4Object.GetTCP().GetDestinationPort()
		} else if l4Object.GetSCTP() != nil {
			return l4Object.GetSCTP().GetDestinationPort()
		} else if l4Object.GetUDP() != nil {
			return l4Object.GetUDP().GetDestinationPort()
		}
	}
	return 0
}

// Conversion function for []*flow.Workflow to proto defined workload array.
func convertToProtoWorkloads(workloads []*flow.Workload) []*pb.Workload {
	protoWorkloads := []*pb.Workload{}
	for _, wl := range workloads {
		protoWorkload := &pb.Workload{
			Name: wl.Name,
			Kind: wl.Kind,
		}
		protoWorkloads = append(protoWorkloads, protoWorkload)
	}
	return protoWorkloads
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
