// Copyright 2026 Illumio, Inc. All Rights Reserved.

package vpccni

import (
	"context"
	"time"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"

	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// DefaultPollInterval is the default interval for polling logs.
const DefaultPollInterval = 5 * time.Second

// Factory creates VPC CNI flow collector clients.
type Factory struct {
	Logger       *zap.Logger
	FlowSink     collector.FlowSink
	K8sClient    kubernetes.Interface
	PollInterval time.Duration
}

// NewFlowCollector creates a new VPC CNI flow collector.
func (f *Factory) NewFlowCollector(_ context.Context) (stream.FlowCollector, error) {
	pollInterval := f.PollInterval
	if pollInterval == 0 {
		pollInterval = DefaultPollInterval
	}

	return &vpccniClient{
		logger:       f.Logger,
		flowSink:     f.FlowSink,
		k8sClient:    f.K8sClient,
		pollInterval: pollInterval,
		lastPollTime: make(map[string]time.Time),
	}, nil
}
