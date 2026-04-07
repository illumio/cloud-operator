// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/k8sclient"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// Verify Factory implements stream.StreamClientFactory.
var _ stream.StreamClientFactory = (*Factory)(nil)

// flowCollectorTypeGetter provides access to the determined flow collector type.
type flowCollectorTypeGetter interface {
	GetFlowCollectorType() pb.FlowCollector
}

// Factory creates resources stream clients.
type Factory struct {
	Logger                *zap.Logger
	K8sClient             k8sclient.Client
	Stats                 *stream.Stats
	FlowCollectorProvider flowCollectorTypeGetter
}

// NewStreamClient creates a new resources stream client.
func (f *Factory) NewStreamClient(ctx context.Context, grpcConn grpc.ClientConnInterface) (stream.StreamClient, error) {
	grpcClient := pb.NewKubernetesInfoServiceClient(grpcConn)
	grpcStream, err := grpcClient.SendKubernetesResources(ctx)
	if err != nil {
		f.Logger.Error("Failed to connect to resources stream", zap.Error(err))

		return nil, err
	}

	// Get flow collector type from provider
	flowCollector := pb.FlowCollector_FLOW_COLLECTOR_UNSPECIFIED
	if f.FlowCollectorProvider != nil {
		flowCollector = f.FlowCollectorProvider.GetFlowCollectorType()
	}

	return &resourcesClient{
		grpcStream:    grpcStream,
		logger:        f.Logger,
		k8sClient:     f.K8sClient,
		stats:         f.Stats,
		flowCollector: flowCollector,
	}, nil
}

// Name returns the stream name for logging.
func (f *Factory) Name() string {
	return "SendKubernetesResources"
}
