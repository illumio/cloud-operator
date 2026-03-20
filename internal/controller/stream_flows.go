// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/hubble"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

// flowSinkAdapter adapts streamManager to implement the collector.FlowSink interface.
type flowSinkAdapter struct {
	flowCache *FlowCache
	stats     *StreamStats
}

// CacheFlow caches a flow in the flow cache.
func (f *flowSinkAdapter) CacheFlow(ctx context.Context, flow any) error {
	pbFlow, ok := flow.(pb.Flow)
	if !ok {
		return errors.New("flow is not a valid pb.Flow type")
	}

	return f.flowCache.CacheFlow(ctx, pbFlow)
}

// IncrementFlowsReceived increments the flows received counter.
func (f *flowSinkAdapter) IncrementFlowsReceived() {
	f.stats.IncrementFlowsReceived()
}

// newFlowSinkAdapter creates a new FlowSink adapter from streamManager.
func (sm *streamManager) newFlowSinkAdapter() *flowSinkAdapter {
	return &flowSinkAdapter{
		flowCache: sm.FlowCache,
		stats:     sm.stats,
	}
}

// startFlowCacheOutReader starts a goroutine that reads flows from the flow cache
// and sends them to the cloud secure. Also sends keepalives at the configured period.
func (sm *streamManager) startFlowCacheOutReader(ctx context.Context, logger *zap.Logger, keepalivePeriod time.Duration) error {
	ticker := time.NewTicker(jitterTime(keepalivePeriod, 0.10))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			err := sm.sendKeepalive(logger, STREAM_NETWORK_FLOWS)
			if err != nil {
				return err
			}
		case flow := <-sm.FlowCache.outFlows:
			err := sm.sendNetworkFlowRequest(logger, flow)
			if err != nil {
				return err
			}

			sm.stats.IncrementFlowsSentToClusterSync()
		}
	}
}

// findHubbleRelay returns a *collector.CiliumFlowCollector if hubble relay is found in the given namespace.
func (sm *streamManager) findHubbleRelay(ctx context.Context, logger *zap.Logger) *collector.CiliumFlowCollector {
	clientset, err := NewClientSet()
	if err != nil {
		logger.Error("Failed to create clientset for Cilium discovery", zap.Error(err))

		return nil
	}

	ciliumFlowCollector, err := collector.NewCiliumFlowCollector(ctx, logger, clientset, sm.streamClient.ciliumNamespaces, sm.streamClient.tlsAuthProperties)
	if err != nil {
		logger.Error("Failed to create Cilium flow collector", zap.Error(err))

		return nil
	}

	return ciliumFlowCollector
}

// StreamCiliumNetworkFlows handles the cilium network flow stream.
func (sm *streamManager) StreamCiliumNetworkFlows(ctx context.Context, logger *zap.Logger) error {
	// TODO: Add logic for a discoveribility function to decide which CNI to use.
	ciliumFlowCollector := sm.findHubbleRelay(ctx, logger)
	if ciliumFlowCollector == nil {
		logger.Info("Failed to initialize Cilium Hubble Relay flow collector; disabling flow collector")

		return errors.New("hubble relay cannot be found")
	}

	err := ciliumFlowCollector.ExportCiliumFlows(ctx, sm.newFlowSinkAdapter())
	if err != nil {
		if errors.Is(err, tls.ErrTLSALPNHandshakeFailed) || errors.Is(err, tls.ErrNoTLSHandshakeFailed) {
			logger.Debug("Network flow collection from Hubble Relay interrupted due to failing TLS handshake; will retry connecting", zap.Error(err))
		} else {
			logger.Warn("Network flow collection from Hubble Relay interrupted; will retry connecting", zap.Error(err))
		}

		sm.disableSubsystemCausingError(err, logger)

		return err
	}

	return nil
}

// StreamFalcoNetworkFlows handles the falco network flow stream.
func (sm *streamManager) StreamFalcoNetworkFlows(ctx context.Context, logger *zap.Logger) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case falcoFlow := <-sm.streamClient.falcoEventChan:
			if !collector.FilterIllumioTraffic(falcoFlow) {
				continue
			}

			// Extract the relevant part of the output string
			match := reIllumioTraffic.FindStringSubmatch(falcoFlow)
			if len(match) < 2 {
				continue
			}

			convertedFiveTupleFlow, err := collector.ParsePodNetworkInfo(match[1])
			if convertedFiveTupleFlow == nil {
				// If the event can't be parsed, consider that it's not a flow event and just ignore it.
				// If the event has bad ports in any way ignore it.
				// If the event has an incomplete L3/L4 layer lets just ignore it.
				continue
			} else if err != nil {
				logger.Error("Failed to parse Falco event into flow", zap.Error(err))

				return err
			}

			err = sm.FlowCache.CacheFlow(ctx, convertedFiveTupleFlow)
			if err != nil {
				logger.Error("Failed to cache flow", zap.Error(err))

				return err
			}

			sm.stats.IncrementFlowsReceived()
		}
	}
}

// StreamOVNKNetworkFlows handles the OVN-K network flow stream.
func (sm *streamManager) StreamOVNKNetworkFlows(ctx context.Context, logger *zap.Logger) error {
	ovnkCollector := collector.NewOVNKCollector(logger, sm.streamClient.ipfixCollectorPort, sm.newFlowSinkAdapter())

	err := ovnkCollector.StartIPFIXCollector(ctx)
	if err != nil {
		logger.Error("Failed to listen for OVN-K IPFIX flows", zap.Error(err))

		return err
	}

	return nil
}

// connectNetworkFlowsStream establishes the network flows stream connection to
// the server and stores the resulting stream client on the streamManager.
func (sm *streamManager) connectNetworkFlowsStream(ctx context.Context, logger *zap.Logger) error {
	sendNetworkFlowsStream, err := sm.streamClient.client.SendKubernetesNetworkFlows(ctx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))

		return err
	}

	sm.streamClient.networkFlowsStream = sendNetworkFlowsStream

	return nil
}

// connectAndStreamCiliumNetworkFlows creates networkFlowsStream client and
// begins the streaming of network flows.
func (sm *streamManager) connectAndStreamCiliumNetworkFlows(logger *zap.Logger, _ time.Duration) error {
	ciliumCtx, ciliumCancel := context.WithCancel(context.Background())
	defer ciliumCancel()

	err := sm.StreamCiliumNetworkFlows(ciliumCtx, logger)
	if err != nil {
		if errors.Is(err, hubble.ErrHubbleNotFound) || errors.Is(err, hubble.ErrNoPortsAvailable) {
			logger.Warn("Disabling Cilium flow collection", zap.Error(err))

			return ErrStopRetries
		}

		return err
	}

	return nil
}

// connectAndStreamFalcoNetworkFlows creates networkFlowsStream client and
// begins the streaming of network flows.
func (sm *streamManager) connectAndStreamFalcoNetworkFlows(logger *zap.Logger, _ time.Duration) error {
	falcoCtx, falcoCancel := context.WithCancel(context.Background())
	defer falcoCancel()

	err := sm.StreamFalcoNetworkFlows(falcoCtx, logger)
	if err != nil {
		logger.Error("Failed to stream Falco network flows", zap.Error(err))

		return err
	}

	return nil
}

// connectAndStreamOVNKNetworkFlows creates OVN-K networkFlowsStream client and
// begins the streaming of OVN-K network flows.
func (sm *streamManager) connectAndStreamOVNKNetworkFlows(logger *zap.Logger, _ time.Duration) error {
	ovnkContext, ovnkCancel := context.WithCancel(context.Background())
	defer ovnkCancel()

	err := sm.StreamOVNKNetworkFlows(ovnkContext, logger)
	if err != nil {
		logger.Error("Failed to stream OVN-K network flows", zap.Error(err))

		return err
	}

	return nil
}

// determineFlowCollector determines the flow collector type and returns the flow collector type, stream function, and the corresponding networkFlowsDone channel.
func determineFlowCollector(ctx context.Context, logger *zap.Logger, sm *streamManager, envMap EnvironmentConfig, clientset kubernetes.Interface) (pb.FlowCollector, func(*zap.Logger, time.Duration) error, chan struct{}) {
	switch {
	case sm.findHubbleRelay(ctx, logger) != nil && !sm.streamClient.disableNetworkFlowsCilium:
		sm.streamClient.tlsAuthProperties.DisableALPN = false
		sm.streamClient.tlsAuthProperties.DisableTLS = false

		return pb.FlowCollector_FLOW_COLLECTOR_CILIUM, sm.connectAndStreamCiliumNetworkFlows, make(chan struct{})
	case collector.IsOVNKDeployed(ctx, logger, envMap.OVNKNamespace, clientset):
		return pb.FlowCollector_FLOW_COLLECTOR_OVNK, sm.connectAndStreamOVNKNetworkFlows, make(chan struct{})
	default:
		return pb.FlowCollector_FLOW_COLLECTOR_FALCO, sm.connectAndStreamFalcoNetworkFlows, make(chan struct{})
	}
}
