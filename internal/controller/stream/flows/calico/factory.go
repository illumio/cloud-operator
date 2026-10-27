// Copyright 2026 Illumio, Inc. All Rights Reserved.

package calico

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

// Factory creates Calico Enterprise flow collector clients.
type Factory struct {
	Logger     *zap.Logger
	SyslogPort string
	SyslogTLS  collector.CalicoSyslogTLS
	FlowSink   collector.FlowSink
}

// NewCollector creates a new Calico Enterprise flow collector.
func (f *Factory) NewCollector(_ context.Context) (flowCollector, error) {
	return &calicoClient{
		logger:     f.Logger,
		syslogPort: f.SyslogPort,
		syslogTLS:  f.SyslogTLS,
		flowSink:   f.FlowSink,
	}, nil
}
