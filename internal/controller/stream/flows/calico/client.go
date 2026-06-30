// Copyright 2026 Illumio, Inc. All Rights Reserved.

package calico

import (
	"context"

	"go.uber.org/zap"

	"github.com/illumio/cloud-operator/internal/controller/collector"
)

// calicoClient implements FlowCollector for Calico Enterprise flow collection.
type calicoClient struct {
	logger     *zap.Logger
	syslogPort string
	flowSink   collector.FlowSink
}

// Run collects flows via the Felix syslog flow reporter from Calico Enterprise.
func (c *calicoClient) Run(ctx context.Context) error {
	calicoCollector := collector.NewCalicoCollector(c.logger, c.syslogPort, c.flowSink)

	err := calicoCollector.RunSyslogCollector(ctx)
	if err != nil {
		c.logger.Error("Failed to listen for Calico Enterprise syslog flows", zap.Error(err))

		return err
	}

	return nil
}
