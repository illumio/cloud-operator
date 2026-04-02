// Copyright 2026 Illumio, Inc. All Rights Reserved.

package logs

import (
	"context"
	"fmt"

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
	Conn               *grpc.ClientConn
}

// NewStreamClient creates a new logs stream client.
func (f *Factory) NewStreamClient(ctx context.Context, grpcClient pb.KubernetesInfoServiceClient) (stream.StreamClient, error) {
	grpcStream, err := grpcClient.SendLogs(ctx)
	if err != nil {
		f.Logger.Error("Failed to connect to logs stream", zap.Error(err))

		return nil, err
	}

	return &logsClient{
		stream:             grpcStream,
		conn:               f.Conn,
		logger:             f.Logger,
		bufferedGrpcSyncer: f.BufferedGrpcSyncer,
	}, nil
}

// Name returns the stream name for logging.
func (f *Factory) Name() string {
	return "SendLogs"
}

// SetConn sets the gRPC connection on the factory.
// This is called after the connection is established in runStreamsOnce.
func (f *Factory) SetConn(conn any) {
	if conn == nil {
		f.Logger.Error("SetConn called with nil connection")

		return
	}

	c, ok := conn.(*grpc.ClientConn)
	if !ok {
		f.Logger.Error("SetConn: unexpected connection type", zap.String("type", fmt.Sprintf("%T", conn)))

		return
	}

	f.Conn = c
}
