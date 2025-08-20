// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// CalicoFlowCollector collects flows from Calico running in this cluster.
type CalicoFlowCollector struct {
	logger          *zap.Logger
	client          kubernetes.Interface
	calicoNamespace string
}

const (
	calicoServiceName string = "calico-typha"
)

var (
	ErrCalicoNotFound         = errors.New("calico service not found; disabling Calico flow collection")
	ErrCalicoNoPortsAvailable = errors.New("calico service has no ports; disabling Calico flow collection")
)

// discoverCalicoAddress uses a kubernetes clientset in order to discover the address of the calico-typha service.
func discoverCalicoAddress(ctx context.Context, calicoNamespace string, clientset kubernetes.Interface) (string, error) {
	service, err := clientset.CoreV1().Services(calicoNamespace).Get(ctx, calicoServiceName, metav1.GetOptions{})
	if err != nil {
		return "", ErrCalicoNotFound
	}

	if len(service.Spec.Ports) == 0 {
		return "", ErrCalicoNoPortsAvailable
	}

	address := fmt.Sprintf("%s:%d", service.Spec.ClusterIP, service.Spec.Ports[0].Port)
	return address, nil
}

// newCalicoFlowCollector verifies Calico service availability and returns a collector.
func newCalicoFlowCollector(ctx context.Context, logger *zap.Logger, calicoNamespace string) (*CalicoFlowCollector, error) {
	clientset, err := NewClientSet()
	if err != nil {
		return nil, fmt.Errorf("failed to create client set: %w", err)
	}

	// Ensure calico-typha service exists and has ports; this allows caller to decide disabling.
	svc, err := clientset.CoreV1().Services(calicoNamespace).Get(ctx, calicoServiceName, metav1.GetOptions{})
	if err != nil {
		return nil, ErrCalicoNotFound
	}
	if len(svc.Spec.Ports) == 0 {
		return nil, ErrCalicoNoPortsAvailable
	}

	return &CalicoFlowCollector{
		logger:          logger,
		client:          clientset,
		calicoNamespace: calicoNamespace,
	}, nil
}


// convertCalicoFlow converts a Calico flow to a CalicoFlow object
// Note: After regenerating the protobuf code, this will return *pb.CalicoFlow
func convertCalicoFlow(flow interface{}) interface{} {
	// In a real implementation, this would convert from Calico's flow format to our CalicoFlow format
	// For now, we'll return a placeholder implementation
	return nil
}

// exportCalicoFlows makes one stream gRPC call to Calico to collect, convert, and export flows into the given stream.
func (fm *CalicoFlowCollector) exportCalicoFlows(ctx context.Context, sm *streamManager) error {
	// In a real implementation, this would connect to Calico's API, collect flows, and send them to the stream
	// For now, we'll just log that we're collecting flows
	fm.logger.Info("Collecting flows from Calico", zap.String("namespace", fm.calicoNamespace))

	// This is a placeholder implementation that just waits for the context to be done
	<-ctx.Done()
	return ctx.Err()
}
