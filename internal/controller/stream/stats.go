// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	"context"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// Stats tracks statistics for flows and resource mutations.
// All counters are safe for concurrent access.
type Stats struct {
	flowsReceived             atomic.Uint64
	flowsSentToClusterSync    atomic.Uint64
	resourceMutations         atomic.Uint64
	configuredObjectMutations atomic.Uint64

	// EKS Auto Mode node-proxy log collection counters.
	autoModeNodesObserved atomic.Uint64 // last observed count of Auto Mode nodes (gauge-like)
	autoModePollErrors    atomic.Uint64 // per-node / list poll errors since last report
}

// NewStats creates a new Stats instance.
func NewStats() *Stats {
	return &Stats{}
}

// IncrementFlowsReceived increments the count of flows received from CNI collectors.
func (s *Stats) IncrementFlowsReceived() {
	s.flowsReceived.Add(1)
}

// IncrementFlowsSentToClusterSync increments the count of flows sent to k8sclustersync.
func (s *Stats) IncrementFlowsSentToClusterSync() {
	s.flowsSentToClusterSync.Add(1)
}

// IncrementResourceMutations increments the count of resource mutations.
func (s *Stats) IncrementResourceMutations() {
	s.resourceMutations.Add(1)
}

// IncrementConfiguredObjectMutations increments the count of configured object mutations.
func (s *Stats) IncrementConfiguredObjectMutations() {
	s.configuredObjectMutations.Add(1)
}

// SetAutoModeNodesObserved records the most recent count of EKS Auto Mode nodes
// seen in a poll cycle. It is a gauge (overwritten), not a running total.
func (s *Stats) SetAutoModeNodesObserved(n int) {
	if n < 0 {
		n = 0
	}

	s.autoModeNodesObserved.Store(uint64(n))
}

// IncrementAutoModePollErrors increments the count of EKS Auto Mode poll errors.
func (s *Stats) IncrementAutoModePollErrors() {
	s.autoModePollErrors.Add(1)
}

// GetAndResetStats returns the current stats and resets all counters to zero.
func (s *Stats) GetAndResetStats() (flowsReceived, flowsSent, resourceMutations, configuredObjectMutations uint64) {
	flowsReceived = s.flowsReceived.Swap(0)
	flowsSent = s.flowsSentToClusterSync.Swap(0)
	resourceMutations = s.resourceMutations.Swap(0)
	configuredObjectMutations = s.configuredObjectMutations.Swap(0)

	return
}

// GetAndResetAutoModeStats returns the EKS Auto Mode counters. The node count is
// a gauge (read without reset); the poll-error count is reset to zero.
func (s *Stats) GetAndResetAutoModeStats() (nodesObserved, pollErrors uint64) {
	nodesObserved = s.autoModeNodesObserved.Load()
	pollErrors = s.autoModePollErrors.Swap(0)

	return
}

// StartStatsLogger starts a goroutine that logs stream statistics at the configured period.
func StartStatsLogger(ctx context.Context, logger *zap.Logger, stats *Stats, period time.Duration) {
	if stats == nil || logger == nil {
		return
	}

	if period <= 0 {
		logger.Info("Stream stats logging disabled (period <= 0)")

		return
	}

	go func() {
		ticker := time.NewTicker(period)
		defer ticker.Stop()

		logger.Info("Stream stats logger started", zap.Duration("period", period))

		for {
			select {
			case <-ctx.Done():
				logger.Info("Stream stats logger stopped")

				return
			case <-ticker.C:
				flowsReceived, flowsSent, resourceMutations, configuredObjectMutations := stats.GetAndResetStats()
				autoModeNodes, autoModePollErrors := stats.GetAndResetAutoModeStats()
				logger.Info("Stream statistics",
					zap.Duration("period", period),
					zap.Uint64("flows_received", flowsReceived),
					zap.Uint64("flows_sent_to_cluster_sync", flowsSent),
					zap.Uint64("resource_mutations", resourceMutations),
					zap.Uint64("configured_object_mutations", configuredObjectMutations),
					zap.Uint64("eks_auto_mode_nodes_observed", autoModeNodes),
					zap.Uint64("eks_auto_mode_poll_errors", autoModePollErrors),
				)
			}
		}
	}()
}
