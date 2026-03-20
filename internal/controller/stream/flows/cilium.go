// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/hubble"
	"github.com/illumio/cloud-operator/internal/controller/k8sclient"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

// FindHubbleRelay returns a *collector.CiliumFlowCollector if hubble relay is found.
func FindHubbleRelay(ctx context.Context, logger *zap.Logger, sm *stream.Manager) *collector.CiliumFlowCollector {
	clientset, err := k8sclient.NewClientSet()
	if err != nil {
		logger.Error("Failed to create clientset for Cilium discovery", zap.Error(err))

		return nil
	}

	ciliumFlowCollector, err := collector.NewCiliumFlowCollector(ctx, logger, clientset, sm.Client.CiliumNamespaces, sm.Client.TlsAuthProperties)
	if err != nil {
		logger.Error("Failed to create Cilium flow collector", zap.Error(err))

		return nil
	}

	return ciliumFlowCollector
}

// StreamCilium handles the cilium network flow stream.
func StreamCilium(ctx context.Context, sm *stream.Manager, logger *zap.Logger) error {
	ciliumFlowCollector := FindHubbleRelay(ctx, logger, sm)
	if ciliumFlowCollector == nil {
		logger.Info("Failed to initialize Cilium Hubble Relay flow collector; disabling flow collector")

		return errors.New("hubble relay cannot be found")
	}

	err := ciliumFlowCollector.ExportCiliumFlows(ctx, NewFlowSinkAdapter(sm))
	if err != nil {
		if errors.Is(err, tls.ErrTLSALPNHandshakeFailed) || errors.Is(err, tls.ErrNoTLSHandshakeFailed) {
			logger.Debug("Network flow collection from Hubble Relay interrupted due to failing TLS handshake; will retry connecting", zap.Error(err))
		} else {
			logger.Warn("Network flow collection from Hubble Relay interrupted; will retry connecting", zap.Error(err))
		}

		disableSubsystemCausingError(sm, err, logger)

		return err
	}

	return nil
}

// ConnectAndStreamCilium creates networkFlowsStream client and begins streaming Cilium flows.
func ConnectAndStreamCilium(sm *stream.Manager, logger *zap.Logger, _ time.Duration) error {
	ciliumCtx, ciliumCancel := context.WithCancel(context.Background())
	defer ciliumCancel()

	err := StreamCilium(ciliumCtx, sm, logger)
	if err != nil {
		if errors.Is(err, hubble.ErrHubbleNotFound) || errors.Is(err, hubble.ErrNoPortsAvailable) {
			logger.Warn("Disabling Cilium flow collection", zap.Error(err))

			return stream.ErrStopRetries
		}

		return err
	}

	return nil
}

// disableSubsystemCausingError mutates the stream.Manager by inspecting the error.
func disableSubsystemCausingError(sm *stream.Manager, err error, logger *zap.Logger) {
	switch {
	case errors.Is(err, tls.ErrTLSALPNHandshakeFailed):
		logger.Info("Disabling ALPN for Hubble Relay connection; will retry connecting")

		sm.Client.TlsAuthProperties.DisableALPN = true
	case errors.Is(err, tls.ErrNoTLSHandshakeFailed):
		logger.Info("Disabling TLS for Hubble Relay connection; will retry connecting")

		sm.Client.TlsAuthProperties.DisableTLS = true
	default:
		logger.Warn("Disabling network flow collection from Hubble Relay due to unrecoverable error", zap.Error(err))

		sm.Client.DisableNetworkFlowsCilium = true
	}
}
