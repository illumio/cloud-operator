// Copyright 2026 Illumio, Inc. All Rights Reserved.

package ovnk

import (
	"context"

	"go.uber.org/zap"

	"github.com/illumio/cloud-operator/internal/controller/collector"
)

// Factory creates OVN-K flow collector clients.
type Factory struct {
	Logger             *zap.Logger
	IPFIXCollectorPort string
	FlowSink           collector.FlowSink
}

// NewFlowCollector creates a new OVN-K flow collector.
func (f *Factory) NewFlowCollector(_ context.Context) (*ovnkClient, error) {
	return &ovnkClient{
		logger:             f.Logger,
		ipfixCollectorPort: f.IPFIXCollectorPort,
		flowSink:           f.FlowSink,
	}, nil
}
