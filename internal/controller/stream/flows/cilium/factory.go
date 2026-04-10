// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cilium

import (
	"context"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"

	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

// k8sClientGetter provides access to Kubernetes client.
type k8sClientGetter interface {
	GetClientset() kubernetes.Interface
}

// Factory creates Cilium flow collector clients.
type Factory struct {
	Logger           *zap.Logger
	FlowSink         collector.FlowSink
	CiliumNamespaces []string
	TlsAuthProps     *tls.AuthProperties
	K8sClient        k8sClientGetter
}

// NewFlowCollector creates a new Cilium flow collector.
func (f *Factory) NewFlowCollector(_ context.Context) (stream.FlowCollector, error) {
	return &ciliumClient{
		logger:           f.Logger,
		flowSink:         f.FlowSink,
		ciliumNamespaces: f.CiliumNamespaces,
		tlsAuthProps:     f.TlsAuthProps,
		k8sClient:        f.K8sClient,
	}, nil
}
