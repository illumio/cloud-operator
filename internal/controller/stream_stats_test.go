// Copyright 2025 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestNewStreamStats(t *testing.T) {
	stats := NewStreamStats()

	assert.NotNil(t, stats)
	assert.Equal(t, uint64(0), stats.flowsReceived.Load())
	assert.Equal(t, uint64(0), stats.flowsSentToClusterSync.Load())
	assert.Equal(t, uint64(0), stats.resourceMutations.Load())
}

func TestStreamStats_IncrementFlowsReceived(t *testing.T) {
	stats := NewStreamStats()

	stats.IncrementFlowsReceived()
	assert.Equal(t, uint64(1), stats.flowsReceived.Load())

	stats.IncrementFlowsReceived()
	stats.IncrementFlowsReceived()
	assert.Equal(t, uint64(3), stats.flowsReceived.Load())
}

func TestStreamStats_IncrementFlowsSentToClusterSync(t *testing.T) {
	stats := NewStreamStats()

	stats.IncrementFlowsSentToClusterSync()
	assert.Equal(t, uint64(1), stats.flowsSentToClusterSync.Load())

	stats.IncrementFlowsSentToClusterSync()
	stats.IncrementFlowsSentToClusterSync()
	assert.Equal(t, uint64(3), stats.flowsSentToClusterSync.Load())
}

func TestStreamStats_IncrementResourceMutations(t *testing.T) {
	stats := NewStreamStats()

	stats.IncrementResourceMutations()
	assert.Equal(t, uint64(1), stats.resourceMutations.Load())

	stats.IncrementResourceMutations()
	stats.IncrementResourceMutations()
	assert.Equal(t, uint64(3), stats.resourceMutations.Load())
}

func TestStreamStats_GetAndResetStats(t *testing.T) {
	stats := NewStreamStats()

	// Increment some stats
	for range 5 {
		stats.IncrementFlowsReceived()
	}

	for range 3 {
		stats.IncrementFlowsSentToClusterSync()
	}

	for range 7 {
		stats.IncrementResourceMutations()
	}

	// Get and reset
	flowsReceived, flowsSent, mutations := stats.GetAndResetStats()

	assert.Equal(t, uint64(5), flowsReceived)
	assert.Equal(t, uint64(3), flowsSent)
	assert.Equal(t, uint64(7), mutations)

	// Verify counters are reset to zero
	assert.Equal(t, uint64(0), stats.flowsReceived.Load())
	assert.Equal(t, uint64(0), stats.flowsSentToClusterSync.Load())
	assert.Equal(t, uint64(0), stats.resourceMutations.Load())
}

func TestStreamStats_GetAndResetStats_EmptyStats(t *testing.T) {
	stats := NewStreamStats()

	flowsReceived, flowsSent, mutations := stats.GetAndResetStats()

	assert.Equal(t, uint64(0), flowsReceived)
	assert.Equal(t, uint64(0), flowsSent)
	assert.Equal(t, uint64(0), mutations)
}

func TestStreamStats_ConcurrentAccess(t *testing.T) {
	stats := NewStreamStats()

	const iterations uint64 = 1000

	var wg sync.WaitGroup

	wg.Add(3)

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

	wg.Wait()

	flowsReceived, flowsSent, mutations := stats.GetAndResetStats()

	assert.Equal(t, iterations, flowsReceived)
	assert.Equal(t, iterations, flowsSent)
	assert.Equal(t, iterations, mutations)
}

func TestStartStatsLogger_DisabledWithZeroInterval(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	stats := NewStreamStats()
	ctx := context.Background()

	// Should not panic and should return immediately
	StartStatsLogger(ctx, logger, stats, 0)

	// Give it a moment to ensure nothing crashes
	time.Sleep(10 * time.Millisecond)
}

func TestStartStatsLogger_DisabledWithNegativeInterval(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	stats := NewStreamStats()
	ctx := context.Background()

	// Should not panic and should return immediately
	StartStatsLogger(ctx, logger, stats, -1*time.Second)

	// Give it a moment to ensure nothing crashes
	time.Sleep(10 * time.Millisecond)
}

func TestStartStatsLogger_StopsOnContextCancel(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	stats := NewStreamStats()
	ctx, cancel := context.WithCancel(context.Background())

	// Start with a short interval
	StartStatsLogger(ctx, logger, stats, 50*time.Millisecond)

	// Add some stats
	stats.IncrementFlowsReceived()
	stats.IncrementFlowsSentToClusterSync()
	stats.IncrementResourceMutations()

	// Wait for at least one log cycle
	time.Sleep(100 * time.Millisecond)

	// Cancel context
	cancel()

	// Give it time to stop
	time.Sleep(50 * time.Millisecond)

	// After cancellation, stats should have been reset by the logger
	// Add more stats to verify logger is no longer resetting
	stats.IncrementFlowsReceived()
	stats.IncrementFlowsReceived()

	time.Sleep(100 * time.Millisecond)

	// These should still be present since logger stopped
	assert.Equal(t, uint64(2), stats.flowsReceived.Load())
}

func TestStartStatsLogger_LogsAtInterval(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	stats := NewStreamStats()

	ctx, cancel := context.WithCancel(context.Background()) //nolint:modernize // need manual cancel control for testing
	defer cancel()

	// Start with a short interval
	StartStatsLogger(ctx, logger, stats, 50*time.Millisecond)

	// Add some stats
	stats.IncrementFlowsReceived()
	stats.IncrementFlowsReceived()
	stats.IncrementFlowsSentToClusterSync()
	stats.IncrementResourceMutations()

	// Wait for the logger to trigger
	time.Sleep(100 * time.Millisecond)

	// Stats should be reset after the log interval
	assert.Equal(t, uint64(0), stats.flowsReceived.Load())
	assert.Equal(t, uint64(0), stats.flowsSentToClusterSync.Load())
	assert.Equal(t, uint64(0), stats.resourceMutations.Load())
}

func TestStartStatsLogger_NilStats(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	ctx := context.Background()

	// Should not panic with nil stats
	StartStatsLogger(ctx, logger, nil, time.Second)
}

func TestStartStatsLogger_NilLogger(t *testing.T) {
	stats := NewStreamStats()
	ctx := context.Background()

	// Should not panic with nil logger
	StartStatsLogger(ctx, nil, stats, time.Second)
}
