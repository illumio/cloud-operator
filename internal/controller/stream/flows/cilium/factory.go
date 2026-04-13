// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cilium

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

// Factory creates Cilium flow collector clients.
type Factory struct {
	Logger           *zap.Logger
	FlowSink         collector.FlowSink
	CiliumNamespaces []string
	TlsAuthProps     *tls.AuthProperties
	K8sClient        collector.K8sClientGetter
}

// NewCollector creates a new Cilium flow collector.
func (f *Factory) NewCollector(_ context.Context) (flowCollector, error) {
	return &ciliumClient{
		logger:           f.Logger,
		flowSink:         f.FlowSink,
		ciliumNamespaces: f.CiliumNamespaces,
		tlsAuthProps:     f.TlsAuthProps,
		k8sClient:        f.K8sClient,
	}, nil
}
