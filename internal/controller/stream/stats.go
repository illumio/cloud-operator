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

	// autoModeActive is set once when EKS Auto Mode is the active flow collector.
	// The Auto Mode counters below are only meaningful (and only logged) then; on
	// other CNIs (Cilium, OVN-K, standard AWS VPC CNI) they stay unset and are not
	// emitted, so the stats line is not cluttered with fields that are always zero.
	autoModeActive atomic.Bool

	// EKS Auto Mode node-proxy log collection counters.
	autoModeNodesObserved atomic.Uint64 // last observed count of Auto Mode nodes (gauge-like)
	autoModePollErrors    atomic.Uint64 // per-node / list poll errors since last report

	// EKS Auto Mode log-rotation recovery counters (since last report).
	autoModeRotationsDetected      atomic.Uint64 // unseen rotated generations detected across polls
	autoModeRotationRecoveries     atomic.Uint64 // rotated generations recovered (tail processed) successfully
	autoModeRotationRecoveryErrors atomic.Uint64 // rotated generations that failed to recover (open/gzip/read error)
	autoModeRotationGaps           atomic.Uint64 // rotated generations gone before recovery (retention gap)
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

// SetAutoModeActive marks EKS Auto Mode as the active flow collector, enabling the
// Auto Mode counters to be logged. Called once at collector detection.
func (s *Stats) SetAutoModeActive() {
	s.autoModeActive.Store(true)
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

// AddAutoModeRotationsDetected adds n to the count of rotated generations detected
// (unseen rotations found on a poll). Callers pass the number found in one poll.
func (s *Stats) AddAutoModeRotationsDetected(n int) {
	if n <= 0 {
		return
	}

	s.autoModeRotationsDetected.Add(uint64(n))
}

// IncrementAutoModeRotationRecoveries increments the count of rotated generations
// successfully recovered (their tail was processed).
func (s *Stats) IncrementAutoModeRotationRecoveries() {
	s.autoModeRotationRecoveries.Add(1)
}

// IncrementAutoModeRotationRecoveryErrors increments the count of rotated
// generations that failed to recover (open/gzip/read error).
func (s *Stats) IncrementAutoModeRotationRecoveryErrors() {
	s.autoModeRotationRecoveryErrors.Add(1)
}

// IncrementAutoModeRotationGaps increments the count of rotated generations that
// were gone before they could be recovered (retention deleted them; a recovery gap).
func (s *Stats) IncrementAutoModeRotationGaps() {
	s.autoModeRotationGaps.Add(1)
}

// GetAndResetStats returns the current stats and resets all counters to zero.
func (s *Stats) GetAndResetStats() (flowsReceived, flowsSent, resourceMutations, configuredObjectMutations uint64) {
	flowsReceived = s.flowsReceived.Swap(0)
	flowsSent = s.flowsSentToClusterSync.Swap(0)
	resourceMutations = s.resourceMutations.Swap(0)
	configuredObjectMutations = s.configuredObjectMutations.Swap(0)

	return
}

// AutoModeStats holds a snapshot of the EKS Auto Mode counters for one report.
type AutoModeStats struct {
	NodesObserved          uint64 // gauge (not reset)
	PollErrors             uint64 // reset each report
	RotationsDetected      uint64 // reset each report
	RotationRecoveries     uint64 // reset each report
	RotationRecoveryErrors uint64 // reset each report
	RotationGaps           uint64 // reset each report
}

// GetAndResetAutoModeStats returns the EKS Auto Mode counters. The node count is
// a gauge (read without reset); every other counter is reset to zero.
func (s *Stats) GetAndResetAutoModeStats() AutoModeStats {
	return AutoModeStats{
		NodesObserved:          s.autoModeNodesObserved.Load(),
		PollErrors:             s.autoModePollErrors.Swap(0),
		RotationsDetected:      s.autoModeRotationsDetected.Swap(0),
		RotationRecoveries:     s.autoModeRotationRecoveries.Swap(0),
		RotationRecoveryErrors: s.autoModeRotationRecoveryErrors.Swap(0),
		RotationGaps:           s.autoModeRotationGaps.Swap(0),
	}
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

				fields := []zap.Field{
					zap.Duration("period", period),
					zap.Uint64("flows_received", flowsReceived),
					zap.Uint64("flows_sent_to_cluster_sync", flowsSent),
					zap.Uint64("resource_mutations", resourceMutations),
					zap.Uint64("configured_object_mutations", configuredObjectMutations),
				}

				// Only include the EKS Auto Mode counters when Auto Mode is the active
				// collector, so other CNIs don't log a row of permanently-zero fields.
				am := stats.GetAndResetAutoModeStats()
				if stats.autoModeActive.Load() {
					fields = append(fields,
						zap.Uint64("eks_auto_mode_nodes_observed", am.NodesObserved),
						zap.Uint64("eks_auto_mode_poll_errors", am.PollErrors),
						zap.Uint64("eks_auto_mode_rotations_detected", am.RotationsDetected),
						zap.Uint64("eks_auto_mode_rotation_recoveries", am.RotationRecoveries),
						zap.Uint64("eks_auto_mode_rotation_recovery_errors", am.RotationRecoveryErrors),
						zap.Uint64("eks_auto_mode_rotation_gaps", am.RotationGaps),
					)
				}

				logger.Info("Stream statistics", fields...)
			}
		}
	}()
}
