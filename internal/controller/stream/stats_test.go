// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
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
