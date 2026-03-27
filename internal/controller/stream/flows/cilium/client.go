// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cilium

import (
	"context"
	"errors"
	"sync"

	"go.uber.org/zap"

	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/hubble"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

// Verify ciliumClient implements stream.StreamClient.
var _ stream.StreamClient = (*ciliumClient)(nil)

// ciliumClient implements stream.StreamClient for Cilium/Hubble flow collection.
type ciliumClient struct {
	logger           *zap.Logger
	flowSink         collector.FlowSink
	ciliumNamespaces []string
	tlsAuthProps     *tls.AuthProperties
	k8sClient        stream.K8sClientGetter

	mutex  sync.RWMutex
	closed bool
}

// Run collects flows from Cilium Hubble Relay.
func (c *ciliumClient) Run(ctx context.Context) error {
	clientset := c.k8sClient.GetClientset()

	// Initialize TlsAuthProps if nil (default: no TLS/ALPN disabled)
	if c.tlsAuthProps == nil {
		c.tlsAuthProps = &tls.AuthProperties{}
	}

	ciliumFlowCollector, err := collector.NewCiliumFlowCollector(ctx, c.logger, clientset, c.ciliumNamespaces, *c.tlsAuthProps)
	if err != nil {
		c.logger.Error("Failed to create Cilium flow collector", zap.Error(err))

		return err
	}

	if ciliumFlowCollector == nil {
		c.logger.Info("Failed to initialize Cilium Hubble Relay flow collector; disabling flow collector")

		return errors.New("hubble relay cannot be found")
	}

	err = ciliumFlowCollector.ExportCiliumFlows(ctx, c.flowSink)
	if err != nil {
		if errors.Is(err, tls.ErrTLSALPNHandshakeFailed) || errors.Is(err, tls.ErrNoTLSHandshakeFailed) {
			c.logger.Debug("Network flow collection from Hubble Relay interrupted due to failing TLS handshake; will retry connecting", zap.Error(err))
		} else {
			c.logger.Warn("Network flow collection from Hubble Relay interrupted; will retry connecting", zap.Error(err))
		}

		c.disableSubsystemCausingError(err)

		return err
	}

	return nil
}

// disableSubsystemCausingError updates TLS properties based on the error.
func (c *ciliumClient) disableSubsystemCausingError(err error) {
	switch {
	case errors.Is(err, tls.ErrTLSALPNHandshakeFailed):
		c.logger.Info("Disabling ALPN for Hubble Relay connection; will retry connecting")
		c.tlsAuthProps.DisableALPN = true
	case errors.Is(err, tls.ErrNoTLSHandshakeFailed):
		c.logger.Info("Disabling TLS for Hubble Relay connection; will retry connecting")
		c.tlsAuthProps.DisableTLS = true
	default:
		c.logger.Warn("Disabling network flow collection from Hubble Relay due to unrecoverable error", zap.Error(err))
	}
}

// SendKeepalive is a no-op for Cilium flow collection (not a gRPC stream).
func (c *ciliumClient) SendKeepalive(_ context.Context) error {
	return nil
}

// Close marks the client as closed.
func (c *ciliumClient) Close() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.closed = true

	return nil
}

// ShouldStopRetries returns true if retries should stop due to unrecoverable errors.
func ShouldStopRetries(err error) bool {
	return errors.Is(err, hubble.ErrHubbleNotFound) || errors.Is(err, hubble.ErrNoPortsAvailable)
}
