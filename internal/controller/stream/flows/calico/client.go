// Copyright 2026 Illumio, Inc. All Rights Reserved.

package calico

import (
	"context"

	"go.uber.org/zap"

	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/collector/calicoenterprise"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

// calicoClient implements FlowCollector for Calico Enterprise flow collection.
type calicoClient struct {
	logger        *zap.Logger
	syslogPort    string
	syslogTLS     tls.ServerSettings
	flowSink      collector.FlowSink
	recordDropped func()
}

// Run collects flows forwarded over syslog by Calico Enterprise.
func (c *calicoClient) Run(ctx context.Context) error {
	calicoCollector := calicoenterprise.NewCollector(c.logger, c.syslogPort, c.syslogTLS, c.flowSink, c.recordDropped)

	err := calicoCollector.RunSyslogCollector(ctx)
	if err != nil {
		c.logger.Error("Failed to listen for Calico Enterprise syslog flows", zap.Error(err))

		return err
	}

	return nil
}
