// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"

	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// Verify NetworkFlowsFactory implements stream.StreamClientFactory.
var _ stream.StreamClientFactory = (*NetworkFlowsFactory)(nil)

// NetworkFlowsFactory creates network flows stream clients for sending flows to CloudSecure.
type NetworkFlowsFactory struct {
	Logger    *zap.Logger
	FlowCache *stream.FlowCache
	Stats     *stream.Stats
}

// NewStreamClient creates a new network flows stream client.
func (f *NetworkFlowsFactory) NewStreamClient(ctx context.Context, grpcClient pb.KubernetesInfoServiceClient) (stream.StreamClient, error) {
	grpcStream, err := grpcClient.SendKubernetesNetworkFlows(ctx)
	if err != nil {
		f.Logger.Error("Failed to connect to network flows stream", zap.Error(err))

		return nil, err
	}

	return &networkFlowsClient{
		grpcStream: grpcStream,
		logger:     f.Logger,
		flowCache:  f.FlowCache,
		stats:      f.Stats,
	}, nil
}

// Name returns the stream name for logging.
func (f *NetworkFlowsFactory) Name() string {
	return "SendKubernetesNetworkFlows"
}
