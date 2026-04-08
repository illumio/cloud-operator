// Copyright 2026 Illumio, Inc. All Rights Reserved.

package ovnk

import (
	"context"

	"go.uber.org/zap"

	"github.com/illumio/cloud-operator/internal/controller/collector"
)

// ovnkClient implements FlowCollector for OVN-Kubernetes flow collection.
type ovnkClient struct {
	logger             *zap.Logger
	ipfixCollectorPort string
	flowSink           collector.FlowSink
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
