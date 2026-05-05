// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
	"time"

	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/cache"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

// FlowSinkAdapter adapts cache.FlowCache and Stats to implement the collector.FlowSink interface.
type FlowSinkAdapter struct {
	FlowCache *cache.FlowCache
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
func NewFlowSinkAdapter(flowCache *cache.FlowCache, stats *stream.Stats) *FlowSinkAdapter {
	return &FlowSinkAdapter{
		FlowCache: flowCache,
		Stats:     stats,
	}
}

// CollectorConfig holds configuration for determining and creating flow collectors.
type CollectorConfig struct {
	Logger             *zap.Logger
	FlowCache          *cache.FlowCache
	Stats              *stream.Stats
	K8sClient          collector.K8sClientGetter
	CiliumNamespaces   []string
	TlsAuthProps       *tls.AuthProperties
	IPFIXCollectorPort string
	OVNKNamespace      string
	// AWS VPC CNI configuration
	AWSVPCCNIPollingInterval time.Duration
}
