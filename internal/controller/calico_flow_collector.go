// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"errors"
	"fmt"

	//pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	//"github.com/illumio/cloud-operator/internal/controller/goldmine"
	//"github.com/illumio/cloud-operator/internal/pkg/tls"
	//"github.com/projectcalico/calico/v3/lib/clientv3"
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

// newCalicoFlowCollector connects to Calico, sets up a client, and returns a new Collector using it.
func newCalicoFlowCollector(ctx context.Context, logger *zap.Logger, calicoNamespace string) (*CalicoFlowCollector, error) {
	config, err := NewClientSet()
	if err != nil {
		return nil, fmt.Errorf("failed to create new client set: %w", err)
	}

	// Try to discover the Calico Typha service to ensure Calico is present
	if _, err := discoverCalicoAddress(ctx, calicoNamespace, config); err != nil {
		// Propagate well-typed discovery errors so callers can decide whether to disable Calico
		return nil, err
	}

	return &CalicoFlowCollector{
		logger:          logger,
		client:          config,
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
