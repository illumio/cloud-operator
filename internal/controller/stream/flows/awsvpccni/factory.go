// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsvpccni

import (
	"context"
	"time"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"

	"github.com/illumio/cloud-operator/internal/controller/collector"
)

// DefaultPollInterval is the default interval for polling logs (matches kubectl default).
const DefaultPollInterval = 1 * time.Second

// flowCollector is the interface for flow collectors returned by this factory.
// Matches flows.Collector interface via structural typing.
type flowCollector interface {
	Run(ctx context.Context) error
}

// Factory creates VPC CNI flow collector clients.
type Factory struct {
	Logger       *zap.Logger
	FlowSink     collector.FlowSink
	K8sClient    kubernetes.Interface
	PollInterval time.Duration
}

// EffectivePollInterval returns the configured polling interval or its default.
func (f *Factory) EffectivePollInterval() time.Duration {
	if f.PollInterval == 0 {
		return DefaultPollInterval
	}

	return f.PollInterval
}

// NewCollector creates a new VPC CNI flow collector.
func (f *Factory) NewCollector(_ context.Context) (flowCollector, error) {
	return &vpccniClient{
		logger:       f.Logger,
		flowSink:     f.FlowSink,
		k8sClient:    f.K8sClient,
		pollInterval: f.EffectivePollInterval(),
		lastPollTime: make(map[string]time.Time),
	}, nil
}
