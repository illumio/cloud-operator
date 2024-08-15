package controller

import (
	"context"
	"fmt"
	"log"

	observer "github.com/cilium/cilium/api/v1/observer"
	"github.com/go-logr/logr"
	testHelper "github.com/illumio/cloud-operator/internal/controller/test_helper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type FlowManager struct {
	logger logr.Logger
	hc     observer.ObserverClient
}

// discoverHubbleRelayAddress uses a kubernetes clientset in order to discover the address of hubble-relay within kube-system
func discoverHubbleRelayAddress(ctx context.Context, logger logr.Logger, clientset *kubernetes.Clientset) (string, error) {
	service, err := clientset.CoreV1().Services("kube-system").Get(ctx, "hubble-relay", metav1.GetOptions{})
	if err != nil {
		logger.Error(err, "Failed to get Hubble Relay service")
		return "", err
	}

	if len(service.Spec.Ports) == 0 {
		logger.Error(err, "Hubble Relay service has no ports")
		return "", err
	}

	address := fmt.Sprintf("%s:%d", service.Spec.ClusterIP, service.Spec.Ports[0].Port)
	return address, nil
}

// initFlowManager connects to hubble, sets up a observerClient and creates a new FlowManager object
func initFlowManager(ctx context.Context, logger logr.Logger) (FlowManager, error) {
	// Connect to Hubble
	config, err := testHelper.NewClientSet()
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
	return FlowManager{logger: logger, hc: hubbleClient}, nil
}

// listenToFlows and fetches network flows in batches indefinitely
func (fm *FlowManager) listenToFlows(ctx context.Context) error {
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
			fm.logger.Info("Flow recorded:", "Source Pod:", flowObj.GetSource().GetPodName(), "Destination Pod:", flowObj.GetDestination().GetPodName(), "Source Workflow:", flowObj.GetSource().GetWorkloads(), "Destination Workflows:", flowObj.GetDestination().GetWorkloads(), "Source Labels:", flowObj.GetSource().GetLabels(), "Destination Labels:", flowObj.GetDestination().GetLabels(), "END", "\n\n")
			flowObj.GetL4().GetProtocol()
			flowObj.GetL4().GetSCTP().GetDestinationPort()
			flowObj.GetL4().GetSCTP().GetSourcePort()
			flowObj.GetL7().GetHttp().ProtoMessage()
		}
	}
}

// readFlows uses the observerClient to make gRPC calls to hubble-relay and grab the last x amount of flows
func (fm *FlowManager) readFlows(ctx context.Context) ([]*observer.GetFlowsResponse, error) {
	req := &observer.GetFlowsRequest{
		Number: 10,
	}
	observerClient := fm.hc
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
