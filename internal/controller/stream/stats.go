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
	flowsReceived          atomic.Uint64
	flowsSentToClusterSync atomic.Uint64
	resourceMutations      atomic.Uint64
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

// GetAndResetStats returns the current stats and resets all counters to zero.
func (s *Stats) GetAndResetStats() (flowsReceived, flowsSent, mutations uint64) {
	flowsReceived = s.flowsReceived.Swap(0)
	flowsSent = s.flowsSentToClusterSync.Swap(0)
	mutations = s.resourceMutations.Swap(0)

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
