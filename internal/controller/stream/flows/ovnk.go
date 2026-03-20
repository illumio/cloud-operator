// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// StreamOVNK handles the OVN-K network flow stream.
func StreamOVNK(ctx context.Context, sm *stream.Manager, logger *zap.Logger) error {
	ovnkCollector := collector.NewOVNKCollector(logger, sm.Client.IpfixCollectorPort, NewFlowSinkAdapter(sm))

	err := ovnkCollector.StartIPFIXCollector(ctx)
	if err != nil {
		logger.Error("Failed to listen for OVN-K IPFIX flows", zap.Error(err))

		return err
	}

	return nil
}

// ConnectAndStreamOVNK creates OVN-K networkFlowsStream client and begins streaming.
func ConnectAndStreamOVNK(sm *stream.Manager, logger *zap.Logger, _ time.Duration) error {
	ovnkContext, ovnkCancel := context.WithCancel(context.Background())
	defer ovnkCancel()

	err := StreamOVNK(ovnkContext, sm, logger)
	if err != nil {
		logger.Error("Failed to stream OVN-K network flows", zap.Error(err))

		return err
	}

	return nil
}
