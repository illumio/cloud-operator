// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/cache"
)

// Verify NetworkFlowsFactory implements stream.StreamClientFactory.
var _ stream.StreamClientFactory = (*NetworkFlowsFactory)(nil)

// NetworkFlowsFactory creates network flows stream clients for sending flows to CloudSecure.
type NetworkFlowsFactory struct {
	Logger    *zap.Logger
	FlowCache *cache.FlowCache
	Stats     *stream.Stats
	// SendTimeout is how long a Send may block before the stream is reopened.
	// Defaults to DefaultSendTimeout.
	SendTimeout time.Duration
}

// NewStreamClient creates a new network flows stream client.
func (f *NetworkFlowsFactory) NewStreamClient(ctx context.Context, grpcConn grpc.ClientConnInterface) (stream.StreamClient, error) {
	grpcClient := pb.NewKubernetesInfoServiceClient(grpcConn)

	streamCtx, cancel := context.WithCancel(ctx)

	grpcStream, err := grpcClient.SendKubernetesNetworkFlows(streamCtx)
	if err != nil {
		cancel()
		f.Logger.Error("Failed to open network flows stream", zap.Error(err))

		return nil, err
	}

	sendTimeout := f.SendTimeout
	if sendTimeout <= 0 {
		sendTimeout = DefaultSendTimeout
	}

	return &networkFlowsClient{
		grpcStream:   grpcStream,
		logger:       f.Logger,
		flowCache:    f.FlowCache,
		stats:        f.Stats,
		cancelStream: cancel,
		sendTimeout:  sendTimeout,
	}, nil
}

// Name returns the stream name for logging.
func (f *NetworkFlowsFactory) Name() string {
	return "SendKubernetesNetworkFlows"
}
