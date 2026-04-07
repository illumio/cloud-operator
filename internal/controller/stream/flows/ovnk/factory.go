// Copyright 2026 Illumio, Inc. All Rights Reserved.

package ovnk

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// Verify Factory implements stream.StreamClientFactory.
var _ stream.StreamClientFactory = (*Factory)(nil)

// Factory creates OVN-K flow collector clients.
type Factory struct {
	Logger             *zap.Logger
	IPFIXCollectorPort string
	FlowSink           collector.FlowSink
}

// NewStreamClient creates a new OVN-K flow collector client.
// Note: grpcConn is not used since OVN-K reads from IPFIX.
func (f *Factory) NewStreamClient(_ context.Context, _ grpc.ClientConnInterface) (stream.StreamClient, error) {
	return &ovnkClient{
		logger:             f.Logger,
		ipfixCollectorPort: f.IPFIXCollectorPort,
		flowSink:           f.FlowSink,
	}, nil
}

// Name returns the stream name for logging.
func (f *Factory) Name() string {
	return "OVNKFlowCollector"
}
