// Copyright 2026 Illumio, Inc. All Rights Reserved.

package config

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/logging"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// Verify Factory implements stream.StreamClientFactory.
var _ stream.StreamClientFactory = (*Factory)(nil)

// Factory creates configuration stream clients.
type Factory struct {
	Logger             *zap.Logger
	VerboseDebugging   bool
	BufferedGrpcSyncer *logging.BufferedGrpcWriteSyncer
}

// NewStreamClient creates a new configuration stream client.
func (f *Factory) NewStreamClient(ctx context.Context, grpcConn grpc.ClientConnInterface) (stream.StreamClient, error) {
	grpcClient := pb.NewKubernetesInfoServiceClient(grpcConn)

	grpcStream, err := grpcClient.GetConfigurationUpdates(ctx)
	if err != nil {
		f.Logger.Error("Failed to open configuration stream", zap.Error(err))

		return nil, err
	}

	return &configClient{
		stream:             grpcStream,
		logger:             f.Logger,
		verboseDebugging:   f.VerboseDebugging,
		bufferedGrpcSyncer: f.BufferedGrpcSyncer,
	}, nil
}

// Name returns the stream name for logging.
func (f *Factory) Name() string {
	return "GetConfigurationUpdates"
}
