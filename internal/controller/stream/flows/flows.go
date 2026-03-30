// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
	"time"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/pkg/timeutil"
)

// FlowSinkAdapter adapts stream.Manager to implement the collector.FlowSink interface.
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

// NewFlowSinkAdapter creates a new FlowSink adapter from stream.Manager.
func NewFlowSinkAdapter(sm *stream.Manager) *FlowSinkAdapter {
	return &FlowSinkAdapter{
		FlowCache: sm.FlowCache,
		Stats:     sm.Stats,
	}
}

// StartCacheOutReader starts a goroutine that reads flows from the flow cache
// and sends them to the cloud secure.
func StartCacheOutReader(ctx context.Context, sm *stream.Manager, logger *zap.Logger, keepalivePeriod time.Duration) error {
	ticker := time.NewTicker(timeutil.JitterTime(keepalivePeriod, 0.10))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			err := sm.SendKeepalive(logger, stream.TypeNetworkFlows)
			if err != nil {
				return err
			}
		case flow, ok := <-sm.FlowCache.OutFlows:
			if !ok {
				// Flow cache channel closed; exit reader gracefully.
				return nil
			}

			err := sm.SendNetworkFlowRequest(logger, flow)
			if err != nil {
				return err
			}

			sm.Stats.IncrementFlowsSentToClusterSync()
		}
	}
}

// ConnectNetworkFlowsStream establishes the network flows stream connection.
func ConnectNetworkFlowsStream(ctx context.Context, sm *stream.Manager, logger *zap.Logger) error {
	sendNetworkFlowsStream, err := sm.Client.GrpcClient.SendKubernetesNetworkFlows(ctx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))

		return err
	}

	sm.Client.KubernetesNetworkFlowsStream = sendNetworkFlowsStream

	return nil
}

// DetermineFlowCollector determines the flow collector type and returns the appropriate stream function.
func DetermineFlowCollector(ctx context.Context, logger *zap.Logger, sm *stream.Manager, envMap stream.EnvironmentConfig, clientset kubernetes.Interface) (pb.FlowCollector, func(*zap.Logger, time.Duration) error, chan struct{}) {
	switch {
	case FindHubbleRelay(ctx, logger, sm) != nil && !sm.Client.DisableNetworkFlowsCilium:
		sm.Client.TlsAuthProperties.DisableALPN = false
		sm.Client.TlsAuthProperties.DisableTLS = false

		//nolint:contextcheck // context is used for setup, stream functions manage their own context
		return pb.FlowCollector_FLOW_COLLECTOR_CILIUM, func(l *zap.Logger, d time.Duration) error {
			return ConnectAndStreamCilium(sm, l, d)
		}, make(chan struct{})
	case collector.IsOVNKDeployed(ctx, logger, envMap.OVNKNamespace, clientset):
		//nolint:contextcheck // context is used for setup, stream functions manage their own context
		return pb.FlowCollector_FLOW_COLLECTOR_OVNK, func(l *zap.Logger, d time.Duration) error {
			return ConnectAndStreamOVNK(sm, l, d)
		}, make(chan struct{})
	default:
		//nolint:contextcheck // context is used for setup, stream functions manage their own context
		return pb.FlowCollector_FLOW_COLLECTOR_FALCO, func(l *zap.Logger, d time.Duration) error {
			return ConnectAndStreamFalco(sm, l, d)
		}, make(chan struct{})
	}
}
