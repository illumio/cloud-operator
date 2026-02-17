// Copyright 2025 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// StreamStats tracks statistics for flows and resource mutations.
// All counters are safe for concurrent access.
type StreamStats struct {
	// flowsReceived counts flows received from CNI collectors (Cilium/Hubble, Falco, OVN-K).
	flowsReceived atomic.Uint64
	// flowsSentToClusterSync counts flows sent to k8sclustersync via the network flows stream.
	flowsSentToClusterSync atomic.Uint64
	// resourceMutations counts resource mutations sent via the resource stream.
	resourceMutations atomic.Uint64
}

// NewStreamStats creates a new StreamStats instance.
func NewStreamStats() *StreamStats {
	return &StreamStats{}
}

// IncrementFlowsReceived increments the count of flows received from CNI collectors.
func (s *StreamStats) IncrementFlowsReceived() {
	s.flowsReceived.Add(1)
}

// IncrementFlowsSentToClusterSync increments the count of flows sent to k8sclustersync.
func (s *StreamStats) IncrementFlowsSentToClusterSync() {
	s.flowsSentToClusterSync.Add(1)
}

// IncrementResourceMutations increments the count of resource mutations.
func (s *StreamStats) IncrementResourceMutations() {
	s.resourceMutations.Add(1)
}

// GetAndResetStats returns the current stats and resets all counters to zero.
// Note: Each counter is reset atomically, but the three resets are not atomic
// as a group. Increments occurring between individual resets will be counted
// in the next reporting period.
func (s *StreamStats) GetAndResetStats() (flowsReceived, flowsSent, mutations uint64) {
	flowsReceived = s.flowsReceived.Swap(0)
	flowsSent = s.flowsSentToClusterSync.Swap(0)
	mutations = s.resourceMutations.Swap(0)

	return
}

// StartStatsLogger starts a goroutine that logs stream statistics at the configured period.
// If stats or logger is nil, the function returns immediately without starting the logger.
func StartStatsLogger(ctx context.Context, logger *zap.Logger, stats *StreamStats, period time.Duration) {
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
				flowsReceived, flowsSent, mutations := stats.GetAndResetStats()
				logger.Info("Stream statistics",
					zap.Duration("period", period),
					zap.Uint64("flows_received", flowsReceived),
					zap.Uint64("flows_sent_to_cluster_sync", flowsSent),
					zap.Uint64("resource_mutations", mutations),
				)
			}
		}
	}()
}
