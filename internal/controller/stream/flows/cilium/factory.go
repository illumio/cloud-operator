// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cilium

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"k8s.io/client-go/kubernetes"

	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

// Verify Factory implements stream.StreamClientFactory.
var _ stream.StreamClientFactory = (*Factory)(nil)

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

// NewStreamClient creates a new Cilium flow collector client.
// Note: grpcConn is not used since Cilium connects to Hubble Relay, not CloudSecure.
func (f *Factory) NewStreamClient(_ context.Context, _ grpc.ClientConnInterface) (stream.StreamClient, error) {
	return &ciliumClient{
		logger:           f.Logger,
		flowSink:         f.FlowSink,
		ciliumNamespaces: f.CiliumNamespaces,
		tlsAuthProps:     f.TlsAuthProps,
		k8sClient:        f.K8sClient,
	}, nil
}

// Name returns the stream name for logging.
func (f *Factory) Name() string {
	return "CiliumFlowCollector"
}
