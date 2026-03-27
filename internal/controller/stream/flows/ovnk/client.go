// Copyright 2026 Illumio, Inc. All Rights Reserved.

package ovnk

import (
	"context"
	"sync"

	"go.uber.org/zap"

	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// Verify ovnkClient implements stream.StreamClient.
var _ stream.StreamClient = (*ovnkClient)(nil)

// ovnkClient implements stream.StreamClient for OVN-Kubernetes flow collection.
type ovnkClient struct {
	logger             *zap.Logger
	ipfixCollectorPort string
	flowSink           collector.FlowSink

	mutex  sync.RWMutex
	closed bool
}

// Run collects flows via IPFIX from OVN-Kubernetes.
func (c *ovnkClient) Run(ctx context.Context) error {
	ovnkCollector := collector.NewOVNKCollector(c.logger, c.ipfixCollectorPort, c.flowSink)

	err := ovnkCollector.StartIPFIXCollector(ctx)
	if err != nil {
		c.logger.Error("Failed to listen for OVN-K IPFIX flows", zap.Error(err))

		return err
	}

	return nil
}

// SendKeepalive is a no-op for OVN-K flow collection (not a gRPC stream).
func (c *ovnkClient) SendKeepalive(_ context.Context) error {
	return nil
}

// Close marks the client as closed.
func (c *ovnkClient) Close() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.closed = true

	return nil
}
