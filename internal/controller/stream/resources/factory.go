// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"context"

	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/k8sclient"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// Verify Factory implements stream.StreamClientFactory.
var _ stream.StreamClientFactory = (*Factory)(nil)

// Factory creates resources stream clients.
type Factory struct {
	Logger        *zap.Logger
	K8sClient     k8sclient.Client
	Stats         *stream.Stats
	FlowCollector pb.FlowCollector
}

// NewStreamClient creates a new resources stream client.
func (f *Factory) NewStreamClient(ctx context.Context, grpcClient pb.KubernetesInfoServiceClient) (stream.StreamClient, error) {
	grpcStream, err := grpcClient.SendKubernetesResources(ctx)
	if err != nil {
		f.Logger.Error("Failed to connect to resources stream", zap.Error(err))

		return nil, err
	}

	return &resourcesClient{
		grpcStream:    grpcStream,
		logger:        f.Logger,
		k8sClient:     f.K8sClient,
		stats:         f.Stats,
		flowCollector: f.FlowCollector,
	}, nil
}

// Name returns the stream name for logging.
func (f *Factory) Name() string {
	return "SendKubernetesResources"
}

// SetK8sClient sets the Kubernetes client on the factory.
// This is called after the client is created in runStreamsOnce.
func (f *Factory) SetK8sClient(client stream.K8sClientGetter) {
	if c, ok := client.(k8sclient.Client); ok {
		f.K8sClient = c
	}
}

// SetFlowCollector sets the flow collector type on the factory.
// This is called after determining which flow collector to use.
func (f *Factory) SetFlowCollector(collector pb.FlowCollector) {
	f.FlowCollector = collector
}
