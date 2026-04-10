// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

// FlowSinkAdapter adapts stream.FlowCache and Stats to implement the collector.FlowSink interface.
type FlowSinkAdapter struct {
	FlowCache *stream.FlowCache
	Stats     *stream.Stats
}

// CacheFlow caches a flow in the flow cache.
func (f *FlowSinkAdapter) CacheFlow(ctx context.Context, flow pb.Flow) error {
	return f.FlowCache.CacheFlow(ctx, flow)
}

// IncrementFlowsReceived increments the flows received counter.
func (f *FlowSinkAdapter) IncrementFlowsReceived() {
	f.Stats.IncrementFlowsReceived()
}

// NewFlowSinkAdapter creates a new FlowSink adapter.
func NewFlowSinkAdapter(flowCache *stream.FlowCache, stats *stream.Stats) *FlowSinkAdapter {
	return &FlowSinkAdapter{
		FlowCache: flowCache,
		Stats:     stats,
	}
}

// K8sClientGetter provides access to Kubernetes client.
type K8sClientGetter interface {
	GetClientset() kubernetes.Interface
}

// FlowCollectorConfig holds configuration for determining and creating flow collectors.
type FlowCollectorConfig struct {
	Logger             *zap.Logger
	FlowCache          *stream.FlowCache
	Stats              *stream.Stats
	K8sClient          K8sClientGetter
	CiliumNamespaces   []string
	TlsAuthProps       *tls.AuthProperties
	IPFIXCollectorPort string
	OVNKNamespace      string
}
