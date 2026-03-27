// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/cilium"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/falco"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/ovnk"
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

// FlowCollectorConfig holds configuration for determining and creating flow collectors.
type FlowCollectorConfig struct {
	Logger             *zap.Logger
	FlowCache          *stream.FlowCache
	Stats              *stream.Stats
	K8sClient          stream.K8sClientGetter
	CiliumNamespaces   []string
	TlsAuthProps       *tls.AuthProperties
	IPFIXCollectorPort string
	OVNKNamespace      string
	FalcoEventChan     chan string
}

// DetermineFlowCollector determines which flow collector to use and returns the appropriate factory.
func DetermineFlowCollector(ctx context.Context, config FlowCollectorConfig) (pb.FlowCollector, stream.StreamClientFactory) {
	clientset := config.K8sClient.GetClientset()
	flowSink := NewFlowSinkAdapter(config.FlowCache, config.Stats)

	// Check for Cilium/Hubble
	if isCiliumAvailable(ctx, config.Logger, clientset, config.CiliumNamespaces, config.TlsAuthProps) {
		config.Logger.Info("Using Cilium flow collector")

		// Initialize TlsAuthProps if nil so DisableTLS/DisableALPN flags persist across retries
		tlsAuthProps := config.TlsAuthProps
		if tlsAuthProps == nil {
			tlsAuthProps = &tls.AuthProperties{}
		}

		return pb.FlowCollector_FLOW_COLLECTOR_CILIUM, &cilium.Factory{
			Logger:           config.Logger,
			FlowSink:         flowSink,
			CiliumNamespaces: config.CiliumNamespaces,
			TlsAuthProps:     tlsAuthProps,
			K8sClient:        config.K8sClient,
		}
	}

	// Check for OVN-Kubernetes
	if collector.IsOVNKDeployed(ctx, config.Logger, config.OVNKNamespace, clientset) {
		config.Logger.Info("Using OVN-Kubernetes flow collector")

		return pb.FlowCollector_FLOW_COLLECTOR_OVNK, &ovnk.Factory{
			Logger:             config.Logger,
			IPFIXCollectorPort: config.IPFIXCollectorPort,
			FlowSink:           flowSink,
		}
	}

	// Default to Falco
	config.Logger.Info("Using Falco flow collector")

	return pb.FlowCollector_FLOW_COLLECTOR_FALCO, &falco.Factory{
		Logger:         config.Logger,
		FlowCache:      config.FlowCache,
		Stats:          config.Stats,
		FalcoEventChan: config.FalcoEventChan,
	}
}

// isCiliumAvailable checks if Cilium Hubble Relay is available in the cluster.
func isCiliumAvailable(ctx context.Context, logger *zap.Logger, clientset kubernetes.Interface, ciliumNamespaces []string, tlsAuthProps *tls.AuthProperties) bool {
	if tlsAuthProps == nil {
		tlsAuthProps = &tls.AuthProperties{}
	}

	ciliumCollector, err := collector.NewCiliumFlowCollector(ctx, logger, clientset, ciliumNamespaces, *tlsAuthProps)
	if err != nil {
		logger.Debug("Cilium not available", zap.Error(err))

		return false
	}

	return ciliumCollector != nil
}
