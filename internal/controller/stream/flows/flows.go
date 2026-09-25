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

// cacheFlowBlockedWarnThreshold is how long a collector may wait to hand a flow
// to the flow cache before a warning is logged.
const cacheFlowBlockedWarnThreshold = 30 * time.Second

// FlowSinkAdapter adapts cache.FlowCache and Stats to implement the collector.FlowSink interface.
type FlowSinkAdapter struct {
	FlowCache *cache.FlowCache
	Stats     *stream.Stats
	// Logger is optional; when set, a collector blocked on the flow cache is logged.
	Logger *zap.Logger
}

// CacheFlow caches a flow in the flow cache.
func (f *FlowSinkAdapter) CacheFlow(ctx context.Context, flow pb.Flow) error {
	if f.Logger == nil {
		return f.FlowCache.CacheFlow(ctx, flow)
	}

	start := time.Now()
	blockedTimer := time.AfterFunc(cacheFlowBlockedWarnThreshold, func() {
		f.Logger.Warn("Flow collector is blocked waiting for the flow cache; flows are not being drained to CloudSecure",
			zap.Duration("blocked_for", time.Since(start)),
			zap.Int("flow_cache_out_queue_len", len(f.FlowCache.OutFlows)),
		)
	})

	err := f.FlowCache.CacheFlow(ctx, flow)

	if !blockedTimer.Stop() {
		f.Logger.Info("Flow collector unblocked; flow cache accepted flow",
			zap.Duration("blocked_for", time.Since(start)),
			zap.Error(err),
		)
	}

	return err
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

	// EKS Auto Mode node-proxy log collection. Activated by detection alone
	// (the eks.amazonaws.com/compute-type=auto node label); no separate enable
	// flag, since detection is a positive signal that only fires on real Auto
	// Mode clusters.
	AutoModePollInterval           time.Duration
	AutoModeMaxConcurrentNodePolls int
	AutoModeLogPath                string
}
