// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsautomode

import (
	"context"
	"time"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"

	"github.com/illumio/cloud-operator/internal/controller/collector"
)

const (
	// DefaultPollInterval is the default node-proxy log poll interval. The
	// endpoint returns the whole file each request, so this is intentionally
	// slower than the pod-log collector's default to limit load on kubelets.
	DefaultPollInterval = 10 * time.Second

	// DefaultMaxConcurrentNodePolls bounds how many nodes are polled at once so a
	// large cluster does not open one in-flight request per node simultaneously.
	DefaultMaxConcurrentNodePolls = 10
)

// flowCollector matches flows.Collector via structural typing (avoids import cycle).
type flowCollector interface {
	Run(ctx context.Context) error
}

// Factory creates EKS Auto Mode flow collector clients.
type Factory struct {
	Logger                 *zap.Logger
	FlowSink               collector.FlowSink
	K8sClient              kubernetes.Interface
	PollInterval           time.Duration
	MaxConcurrentNodePolls int
	// LogPath overrides the node-local Network Policy Agent log path (relative to
	// the kubelet log root). Empty uses collector.DefaultNetworkPolicyAgentLogPath.
	LogPath string

	// StatsAutoModeNodes, if set, is called each poll cycle with the number of
	// Auto Mode nodes observed. StatsAutoModeErrors, if set, is called on each
	// per-node or list error. Both are optional (nil-safe).
	StatsAutoModeNodes  func(int)
	StatsAutoModeErrors func()

	// Rotation-recovery stats callbacks (all optional / nil-safe):
	//   StatsRotationsDetected(n) - n unseen rotated generations detected on a poll.
	//   StatsRotationRecovered()  - a rotated generation's tail was recovered.
	//   StatsRotationRecoveryErr()- a rotated generation failed to recover.
	//   StatsRotationGap()        - a rotated generation was gone before recovery.
	StatsRotationsDetected   func(int)
	StatsRotationRecovered   func()
	StatsRotationRecoveryErr func()
	StatsRotationGap         func()
}

// NewCollector creates a new EKS Auto Mode flow collector.
func (f *Factory) NewCollector(_ context.Context) (flowCollector, error) {
	pollInterval := f.PollInterval
	if pollInterval <= 0 {
		pollInterval = DefaultPollInterval
	}

	maxConcurrent := f.MaxConcurrentNodePolls
	if maxConcurrent <= 0 {
		maxConcurrent = DefaultMaxConcurrentNodePolls
	}

	return &autoModeClient{
		logger:                   f.Logger,
		flowSink:                 f.FlowSink,
		k8sClient:                f.K8sClient,
		fetcher:                  &restLogFetcher{k8sClient: f.K8sClient, logPath: f.LogPath, logger: f.Logger},
		pollInterval:             pollInterval,
		maxConcurrentPolls:       maxConcurrent,
		checkpoints:              newCheckpointStore(),
		statsAutoModeNodes:       f.StatsAutoModeNodes,
		statsAutoModeErrors:      f.StatsAutoModeErrors,
		statsRotationsDetected:   f.StatsRotationsDetected,
		statsRotationRecovered:   f.StatsRotationRecovered,
		statsRotationRecoveryErr: f.StatsRotationRecoveryErr,
		statsRotationGap:         f.StatsRotationGap,
	}, nil
}
