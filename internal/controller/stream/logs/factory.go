// Copyright 2026 Illumio, Inc. All Rights Reserved.

package logs

import (
	"context"
	"errors"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/logging"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// Verify Factory implements stream.StreamClientFactory.
var _ stream.StreamClientFactory = (*Factory)(nil)

// Factory creates logs stream clients.
type Factory struct {
	Logger             *zap.Logger
	BufferedGrpcSyncer *logging.BufferedGrpcWriteSyncer
}

// NewStreamClient creates a new logs stream client.
func (f *Factory) NewStreamClient(ctx context.Context, grpcConn grpc.ClientConnInterface) (stream.StreamClient, error) {
	grpcClient := pb.NewKubernetesInfoServiceClient(grpcConn)

	grpcStream, err := grpcClient.SendLogs(ctx)
	if err != nil {
		f.Logger.Error("Failed to open logs stream", zap.Error(err))

		return nil, err
	}

	// Type assert to logging.ClientConnInterface for BufferedGrpcWriteSyncer.
	// This works because *grpc.ClientConn implements both grpc.ClientConnInterface
	// and logging.ClientConnInterface.
	conn, ok := grpcConn.(logging.ClientConnInterface)
	if !ok {
		f.Logger.Error("gRPC connection does not implement logging.ClientConnInterface")

		return nil, errors.New("gRPC connection does not implement logging.ClientConnInterface")
	}

	// Create done channel for this session - signals BufferedGrpcSyncer when stream closes
	done := make(chan struct{})
	f.BufferedGrpcSyncer.SetDone(done)

	return &logsClient{
		stream:             grpcStream,
		conn:               conn,
		logger:             f.Logger,
		bufferedGrpcSyncer: f.BufferedGrpcSyncer,
		done:               done,
	}, nil
}

// Name returns the stream name for logging.
func (f *Factory) Name() string {
	return "SendLogs"
}
