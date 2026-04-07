// Copyright 2026 Illumio, Inc. All Rights Reserved.

package falco

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// Verify Factory implements stream.StreamClientFactory.
var _ stream.StreamClientFactory = (*Factory)(nil)

// Factory creates Falco flow collector clients.
type Factory struct {
	Logger         *zap.Logger
	FlowCache      *stream.FlowCache
	Stats          *stream.Stats
	FalcoEventChan chan string
}

// NewStreamClient creates a new Falco flow collector client.
// Note: grpcConn is not used since Falco reads from its event channel.
func (f *Factory) NewStreamClient(_ context.Context, _ grpc.ClientConnInterface) (stream.StreamClient, error) {
	return &falcoClient{
		logger:         f.Logger,
		flowCache:      f.FlowCache,
		stats:          f.Stats,
		falcoEventChan: f.FalcoEventChan,
	}, nil
}

// Name returns the stream name for logging.
func (f *Factory) Name() string {
	return "FalcoFlowCollector"
}
