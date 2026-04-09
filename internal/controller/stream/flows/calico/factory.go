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

// Factory creates Calico Goldmane flow collector clients.
type Factory struct {
	Logger          *zap.Logger
	FlowSink        collector.FlowSink
	CalicoNamespace string
	K8sClient       collector.K8sClientGetter
}

// NewCollector creates a new Calico Goldmane flow collector.
func (f *Factory) NewCollector(_ context.Context) (flowCollector, error) {
	return &calicoClient{
		logger:          f.Logger,
		flowSink:        f.FlowSink,
		calicoNamespace: f.CalicoNamespace,
		k8sClient:       f.K8sClient,
	}, nil
}
