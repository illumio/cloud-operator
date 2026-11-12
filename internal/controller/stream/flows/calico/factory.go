// Copyright 2026 Illumio, Inc. All Rights Reserved.

package calico

import (
	"context"

	"go.uber.org/zap"

	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
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
	SyslogTLS  tls.ServerSettings
	FlowSink   collector.FlowSink
	// RecordDropped is called for each syslog record that is not turned into a flow.
	RecordDropped func()
}

// NewCollector creates a new Calico Enterprise flow collector.
func (f *Factory) NewCollector(_ context.Context) (flowCollector, error) {
	return &calicoClient{
		logger:        f.Logger,
		syslogPort:    f.SyslogPort,
		syslogTLS:     f.SyslogTLS,
		recordDropped: f.RecordDropped,
		flowSink:      f.FlowSink,
	}, nil
}
