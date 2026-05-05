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

// NewCollector creates a new VPC CNI flow collector.
func (f *Factory) NewCollector(_ context.Context) (flowCollector, error) {
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
