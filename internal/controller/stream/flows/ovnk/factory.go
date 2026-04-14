// Copyright 2026 Illumio, Inc. All Rights Reserved.

package ovnk

import (
	"context"

	"go.uber.org/zap"

	"github.com/illumio/cloud-operator/internal/controller/collector"
)

// flowCollector is the interface for flow collectors returned by this factory.
// Matches flows.Collector interface via structural typing.
type flowCollector interface {
	Run(ctx context.Context) error
}

// Factory creates OVN-K flow collector clients.
type Factory struct {
	Logger             *zap.Logger
	IPFIXCollectorPort string
	FlowSink           collector.FlowSink
}

// NewCollector creates a new OVN-K flow collector.
func (f *Factory) NewCollector(_ context.Context) (flowCollector, error) {
	return &ovnkClient{
		logger:             f.Logger,
		ipfixCollectorPort: f.IPFIXCollectorPort,
		flowSink:           f.FlowSink,
	}, nil
}
