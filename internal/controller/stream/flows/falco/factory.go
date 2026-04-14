// Copyright 2026 Illumio, Inc. All Rights Reserved.

package falco

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

// Factory creates Falco flow collector clients.
type Factory struct {
	Logger   *zap.Logger
	FlowSink collector.FlowSink
}

// NewCollector creates a new Falco flow collector.
func (f *Factory) NewCollector(_ context.Context) (flowCollector, error) {
	return &falcoClient{
		logger:   f.Logger,
		flowSink: f.FlowSink,
	}, nil
}
