// Copyright 2026 Illumio, Inc. All Rights Reserved.

package falco

import (
	"context"

	"go.uber.org/zap"

	"github.com/illumio/cloud-operator/internal/controller/collector"
)

// Factory creates Falco flow collector clients.
type Factory struct {
	Logger         *zap.Logger
	FlowSink       collector.FlowSink
	FalcoEventChan chan string
}

// NewFlowCollector creates a new Falco flow collector.
func (f *Factory) NewFlowCollector(_ context.Context) (*falcoClient, error) {
	return &falcoClient{
		logger:         f.Logger,
		flowSink:       f.FlowSink,
		falcoEventChan: f.FalcoEventChan,
	}, nil
}
