// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/pkg/timeutil"
)

// MockFlow implements pb.Flow for testing.
type MockFlow struct {
	startTimestamp time.Time
	key            string
}

func (m *MockFlow) StartTimestamp() time.Time {
	return m.startTimestamp
}

func (m *MockFlow) Key() any {
	return m.key
}

func TestNewFlowSinkAdapter(t *testing.T) {
	outFlows := make(chan pb.Flow, 10)
	flowCache := stream.NewFlowCache(10*time.Second, 100, outFlows)
	stats := stream.NewStats()

	adapter := NewFlowSinkAdapter(flowCache, stats)

	assert.NotNil(t, adapter)
	assert.Equal(t, flowCache, adapter.FlowCache)
	assert.Equal(t, stats, adapter.Stats)
}

func TestFlowSinkAdapter_CacheFlow(t *testing.T) {
	outFlows := make(chan pb.Flow, 10)
	flowCache := stream.NewFlowCache(10*time.Second, 100, outFlows)
	stats := stream.NewStats()

	adapter := &FlowSinkAdapter{
		FlowCache: flowCache,
		Stats:     stats,
	}

	flow := &MockFlow{startTimestamp: time.Now(), key: "test-flow"}
	err := adapter.CacheFlow(context.Background(), flow)
	require.NoError(t, err)
}

func TestFlowSinkAdapter_IncrementFlowsReceived(t *testing.T) {
	stats := stream.NewStats()

	adapter := &FlowSinkAdapter{
		Stats: stats,
	}

	adapter.IncrementFlowsReceived()
	adapter.IncrementFlowsReceived()
	adapter.IncrementFlowsReceived()

	flowsReceived, _, _ := stats.GetAndResetStats()
	assert.Equal(t, uint64(3), flowsReceived)
}

func TestJitterTime(t *testing.T) {
	tests := []struct {
		name         string
		base         time.Duration
		maxJitterPct float64
		minExpected  time.Duration
		maxExpected  time.Duration
	}{
		{
			name:         "10% jitter on 100ms",
			base:         100 * time.Millisecond,
			maxJitterPct: 0.10,
			minExpected:  90 * time.Millisecond,  // base * (1 - maxJitterPct)
			maxExpected:  100 * time.Millisecond, // base * 1
		},
		{
			name:         "20% jitter on 100ms",
			base:         100 * time.Millisecond,
			maxJitterPct: 0.20,
			minExpected:  80 * time.Millisecond,
			maxExpected:  100 * time.Millisecond,
		},
		{
			name:         "no jitter",
			base:         100 * time.Millisecond,
			maxJitterPct: 0.0,
			minExpected:  100 * time.Millisecond,
			maxExpected:  100 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := timeutil.JitterTime(tt.base, tt.maxJitterPct)
			assert.GreaterOrEqual(t, result, tt.minExpected, "result should be >= min")
			assert.LessOrEqual(t, result, tt.maxExpected, "result should be <= max")
		})
	}
}
