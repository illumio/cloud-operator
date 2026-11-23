// Copyright 2026 Illumio, Inc. All Rights Reserved.

package ovnk

import (
	"context"
	"os"

	"go.uber.org/zap"

	"github.com/illumio/cloud-operator/internal/controller/collector"
)

// ovnkClient implements FlowCollector for OVN-Kubernetes flow collection.
type ovnkClient struct {
	logger             *zap.Logger
	ipfixCollectorPort string
	flowSink           collector.FlowSink
	k8sClient          collector.K8sClientGetter
	podNamespace       string
}

// Run collects flows via IPFIX from OVN-Kubernetes.
func (c *ovnkClient) Run(ctx context.Context) error {
	c.startIPFIXExportCheck(ctx)

	ovnkCollector := collector.NewOVNKCollector(c.logger, c.ipfixCollectorPort, c.flowSink)

	err := ovnkCollector.RunIPFIXCollector(ctx)
	if err != nil {
		c.logger.Error("Failed to listen for OVN-K IPFIX flows", zap.Error(err))

		return err
	}

	return nil
}

// startIPFIXExportCheck starts checking, in the background, that OVN-Kubernetes
// exports its flows to this pod. A failure to start the check is only logged:
// it must not prevent flow collection.
func (c *ovnkClient) startIPFIXExportCheck(ctx context.Context) {
	if c.k8sClient == nil {
		return
	}

	// In a pod, the hostname is the pod's name.
	podName, err := os.Hostname()
	if err != nil {
		c.logger.Warn("Failed to get the pod name, so the IPFIX export of OVN-Kubernetes is not checked", zap.Error(err))

		return
	}

	checker, err := collector.NewIPFIXExportChecker(c.logger, c.k8sClient.GetClientset(), c.k8sClient.GetDynamicClient(),
		c.podNamespace, podName, c.ipfixCollectorPort)
	if err != nil {
		c.logger.Warn("Invalid IPFIX collector port, so the IPFIX export of OVN-Kubernetes is not checked", zap.Error(err))

		return
	}

	go checker.Run(ctx)
}
