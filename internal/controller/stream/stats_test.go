// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestNewStats(t *testing.T) {
	stats := NewStats()

	assert.NotNil(t, stats)
	assert.Equal(t, uint64(0), stats.flowsReceived.Load())
	assert.Equal(t, uint64(0), stats.flowsSentToClusterSync.Load())
	assert.Equal(t, uint64(0), stats.resourceMutations.Load())
}

func TestStats_IncrementFlowsReceived(t *testing.T) {
	stats := NewStats()

	stats.IncrementFlowsReceived()
	assert.Equal(t, uint64(1), stats.flowsReceived.Load())

	stats.IncrementFlowsReceived()
	stats.IncrementFlowsReceived()
	assert.Equal(t, uint64(3), stats.flowsReceived.Load())
}

func TestStats_IncrementFlowsSentToClusterSync(t *testing.T) {
	stats := NewStats()

	stats.IncrementFlowsSentToClusterSync()
	assert.Equal(t, uint64(1), stats.flowsSentToClusterSync.Load())

	stats.IncrementFlowsSentToClusterSync()
	stats.IncrementFlowsSentToClusterSync()
	assert.Equal(t, uint64(3), stats.flowsSentToClusterSync.Load())
}

func TestStats_IncrementResourceMutations(t *testing.T) {
	stats := NewStats()

	stats.IncrementResourceMutations()
	assert.Equal(t, uint64(1), stats.resourceMutations.Load())

	stats.IncrementResourceMutations()
	stats.IncrementResourceMutations()
	assert.Equal(t, uint64(3), stats.resourceMutations.Load())
}

func TestStats_IncrementConfiguredObjectMutations(t *testing.T) {
	stats := NewStats()

	stats.IncrementConfiguredObjectMutations()
	assert.Equal(t, uint64(1), stats.configuredObjectMutations.Load())

	stats.IncrementConfiguredObjectMutations()
	stats.IncrementConfiguredObjectMutations()
	assert.Equal(t, uint64(3), stats.configuredObjectMutations.Load())
}

func TestStats_GetAndResetStats(t *testing.T) {
	stats := NewStats()

	for range 5 {
		stats.IncrementFlowsReceived()
	}

	for range 3 {
		stats.IncrementFlowsSentToClusterSync()
	}

	for range 7 {
		stats.IncrementResourceMutations()
	}

	for range 4 {
		stats.IncrementConfiguredObjectMutations()
	}

	flowsReceived, flowsSent, resourceMutations, configuredObjectMutations := stats.GetAndResetStats()

	assert.Equal(t, uint64(5), flowsReceived)
	assert.Equal(t, uint64(3), flowsSent)
	assert.Equal(t, uint64(7), resourceMutations)
	assert.Equal(t, uint64(4), configuredObjectMutations)

	assert.Equal(t, uint64(0), stats.flowsReceived.Load())
	assert.Equal(t, uint64(0), stats.flowsSentToClusterSync.Load())
	assert.Equal(t, uint64(0), stats.resourceMutations.Load())
	assert.Equal(t, uint64(0), stats.configuredObjectMutations.Load())
}

func TestStats_GetAndResetStats_EmptyStats(t *testing.T) {
	stats := NewStats()

	flowsReceived, flowsSent, resourceMutations, configuredObjectMutations := stats.GetAndResetStats()

	assert.Equal(t, uint64(0), flowsReceived)
	assert.Equal(t, uint64(0), flowsSent)
	assert.Equal(t, uint64(0), resourceMutations)
	assert.Equal(t, uint64(0), configuredObjectMutations)
}

func TestStats_AutoModeCounters(t *testing.T) {
	stats := NewStats()

	stats.SetAutoModeNodesObserved(12)

	for range 3 {
		stats.IncrementAutoModePollErrors()
	}

	am := stats.GetAndResetAutoModeStats()
	assert.Equal(t, uint64(12), am.NodesObserved)
	assert.Equal(t, uint64(3), am.PollErrors)

	// Node count is a gauge (not reset); poll errors reset to zero.
	am = stats.GetAndResetAutoModeStats()
	assert.Equal(t, uint64(12), am.NodesObserved)
	assert.Equal(t, uint64(0), am.PollErrors)

	// Negative node counts are clamped to zero.
	stats.SetAutoModeNodesObserved(-5)
	am = stats.GetAndResetAutoModeStats()
	assert.Equal(t, uint64(0), am.NodesObserved)
}

func TestStats_CalicoRecordsDropped(t *testing.T) {
	stats := NewStats()

	for range 3 {
		stats.IncrementCalicoRecordsDropped()
	}

	assert.Equal(t, uint64(3), stats.GetAndResetCalicoRecordsDropped())
	assert.Equal(t, uint64(0), stats.GetAndResetCalicoRecordsDropped())
}

func TestStats_AutoModeRotationCounters(t *testing.T) {
	stats := NewStats()

	// Active flag is off by default.
	assert.False(t, stats.autoModeActive.Load())
	stats.SetAutoModeActive()
	assert.True(t, stats.autoModeActive.Load())

	stats.AddAutoModeRotationsDetected(3)
	stats.AddAutoModeRotationsDetected(0)  // no-op
	stats.AddAutoModeRotationsDetected(-2) // no-op

	stats.IncrementAutoModeRotationRecoveries()
	stats.IncrementAutoModeRotationRecoveries()
	stats.IncrementAutoModeRotationRecoveryErrors()
	stats.IncrementAutoModeRotationGaps()

	am := stats.GetAndResetAutoModeStats()
	assert.Equal(t, uint64(3), am.RotationsDetected)
	assert.Equal(t, uint64(2), am.RotationRecoveries)
	assert.Equal(t, uint64(1), am.RotationRecoveryErrors)
	assert.Equal(t, uint64(1), am.RotationGaps)

	// All rotation counters reset each report.
	am = stats.GetAndResetAutoModeStats()
	assert.Equal(t, uint64(0), am.RotationsDetected)
	assert.Equal(t, uint64(0), am.RotationRecoveries)
	assert.Equal(t, uint64(0), am.RotationRecoveryErrors)
	assert.Equal(t, uint64(0), am.RotationGaps)
}

func TestStats_ConcurrentAccess(t *testing.T) {
	stats := NewStats()

	const iterations uint64 = 1000

	var wg sync.WaitGroup

	wg.Add(4)

	go func() {
		defer wg.Done()

		for range iterations {
			stats.IncrementFlowsReceived()
		}
	}()

	go func() {
		defer wg.Done()

		for range iterations {
			stats.IncrementFlowsSentToClusterSync()
		}
	}()

	go func() {
		defer wg.Done()

		for range iterations {
			stats.IncrementResourceMutations()
		}
	}()

	go func() {
		defer wg.Done()

		for range iterations {
			stats.IncrementConfiguredObjectMutations()
		}
	}()

	wg.Wait()

	flowsReceived, flowsSent, resourceMutations, configuredObjectMutations := stats.GetAndResetStats()

	assert.Equal(t, iterations, flowsReceived)
	assert.Equal(t, iterations, flowsSent)
	assert.Equal(t, iterations, resourceMutations)
	assert.Equal(t, iterations, configuredObjectMutations)
}

func TestStartStatsLogger_DisabledWithZeroInterval(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	stats := NewStats()
	ctx := context.Background()

	StartStatsLogger(ctx, logger, stats, 0)

	time.Sleep(10 * time.Millisecond)
}

func TestStartStatsLogger_DisabledWithNegativeInterval(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	stats := NewStats()
	ctx := context.Background()

	StartStatsLogger(ctx, logger, stats, -1*time.Second)

	time.Sleep(10 * time.Millisecond)
}

func TestStartStatsLogger_StopsOnContextCancel(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	stats := NewStats()
	ctx, cancel := context.WithCancel(context.Background())

	StartStatsLogger(ctx, logger, stats, 50*time.Millisecond)

	stats.IncrementFlowsReceived()
	stats.IncrementFlowsSentToClusterSync()
	stats.IncrementResourceMutations()

	time.Sleep(100 * time.Millisecond)

	cancel()

	time.Sleep(50 * time.Millisecond)

	stats.IncrementFlowsReceived()
	stats.IncrementFlowsReceived()

	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, uint64(2), stats.flowsReceived.Load())
}

func TestStartStatsLogger_NilStats(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	ctx := context.Background()

	StartStatsLogger(ctx, logger, nil, time.Second)
}

func TestStartStatsLogger_NilLogger(t *testing.T) {
	stats := NewStats()
	ctx := context.Background()

	StartStatsLogger(ctx, nil, stats, time.Second)
}

func TestStartStatsLogger_CalicoRecordsDropped(t *testing.T) {
	tests := map[string]struct {
		calicoActive bool
	}{
		"logged when Calico Enterprise is the active collector": {calicoActive: true},
		"omitted for other collectors":                          {calicoActive: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			core, logs := observer.New(zap.InfoLevel)
			stats := NewStats()

			if tt.calicoActive {
				stats.SetCalicoActive()
			}

			stats.IncrementCalicoRecordsDropped()
			stats.IncrementCalicoRecordsDropped()

			StartStatsLogger(t.Context(), zap.New(core), stats, 20*time.Millisecond)

			require.Eventually(t, func() bool {
				return logs.FilterMessage("Stream statistics").Len() > 0
			}, time.Second, 5*time.Millisecond)

			fields := logs.FilterMessage("Stream statistics").All()[0].ContextMap()

			dropped, found := fields["calico_records_dropped"]
			assert.Equal(t, tt.calicoActive, found)

			if tt.calicoActive {
				assert.Equal(t, uint64(2), dropped)
			}
		})
	}
}
