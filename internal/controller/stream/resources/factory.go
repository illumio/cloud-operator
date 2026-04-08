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

// Factory creates resources stream clients.
type Factory struct {
	Logger            *zap.Logger
	K8sClient         k8sclient.Client
	Stats             *stream.Stats
	FlowCollectorType pb.FlowCollector
}

// NewStreamClient creates a new resources stream client.
func (f *Factory) NewStreamClient(ctx context.Context, grpcConn grpc.ClientConnInterface) (stream.StreamClient, error) {
	grpcClient := pb.NewKubernetesInfoServiceClient(grpcConn)

	grpcStream, err := grpcClient.SendKubernetesResources(ctx)
	if err != nil {
		f.Logger.Error("Failed to open resources stream", zap.Error(err))

		return nil, err
	}

	return &resourcesClient{
		grpcStream:    grpcStream,
		logger:        f.Logger,
		k8sClient:     f.K8sClient,
		stats:         f.Stats,
		flowCollector: f.FlowCollectorType,
	}, nil
}

// Name returns the stream name for logging.
func (f *Factory) Name() string {
	return "SendKubernetesResources"
}
