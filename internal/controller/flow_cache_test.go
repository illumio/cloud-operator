package controller

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

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

func TestNewFlowCache(t *testing.T) {
	outFlows := make(chan pb.Flow)
	c := NewFlowCache(10*time.Second, 100, outFlows)

	assert.NotNil(t, c)
	assert.NotNil(t, c.queue)
	assert.NotNil(t, c.cache)
	assert.Equal(t, 100, c.maxFlows)
	assert.Equal(t, 10*time.Second, c.activeTimeout)
	assert.Equal(t, c.outFlows, outFlows)
	assert.Len(t, c.cache, 0)
	assert.Equal(t, c.queue.Len(), 0)
}

func TestFlowCache_Close(t *testing.T) {
	outFlows := make(chan pb.Flow)
	c := NewFlowCache(10*time.Second, 100, outFlows)

	err := c.Close()
	assert.NoError(t, err)

	_, ok := <-c.inFlows
	assert.False(t, ok, "inFlows channel should be closed")

	_, ok = <-c.outFlows
	assert.False(t, ok, "outFlows channel should be closed")
}

func TestFlowCache_CacheFlow(t *testing.T) {
	outFlows := make(chan pb.Flow, 1)
	c := NewFlowCache(10*time.Second, 100, outFlows)

	ctx := context.Background()
	flow := &MockFlow{startTimestamp: time.Now(), key: "flow1"}

	err := c.CacheFlow(ctx, flow)
	assert.NoError(t, err)

	receivedFlow := <-c.inFlows
	assert.Equal(t, flow, receivedFlow)
}

func TestFlowCache_EvictExpiredFlows(t *testing.T) {
	outFlows := make(chan pb.Flow, 10)
	c := NewFlowCache(10*time.Second, 100, outFlows)

	now := time.Now()
	expiredFlow := &MockFlow{startTimestamp: now.Add(-15 * time.Second), key: "expired"}
	activeFlow := &MockFlow{startTimestamp: now.Add(-5 * time.Second), key: "active"}

	c.queue.PushBack(expiredFlow)
	c.queue.PushBack(activeFlow)
	c.cache[expiredFlow.Key()] = expiredFlow
	c.cache[activeFlow.Key()] = activeFlow

	ctx := context.Background()
	c.evictExpiredFlows(ctx)

	assert.Len(t, c.cache, 1)
	assert.Equal(t, c.queue.Len(), 1)
	assert.Equal(t, activeFlow, c.queue.Front().Value)

	evictedFlow := <-c.outFlows
	assert.Equal(t, expiredFlow, evictedFlow)
}

func TestFlowCache_ShouldSkipFlow(t *testing.T) {
	outFlows := make(chan pb.Flow, 10)
	c := NewFlowCache(10*time.Second, 100, outFlows)

	flow := &MockFlow{startTimestamp: time.Now(), key: "flow1"}
	c.cache[flow.Key()] = flow

	assert.True(t, c.shouldSkipFlow(flow))
}

func TestFlowCache_ShouldEvictOldest(t *testing.T) {
	outFlows := make(chan pb.Flow, 10)
	c := NewFlowCache(10*time.Second, 100, outFlows)

	for i := 0; i < 100; i++ {
		flow := &MockFlow{startTimestamp: time.Now(), key: "flow" + strconv.Itoa(i)}
		c.queue.PushBack(flow)
		c.cache[flow.Key()] = flow
	}

	assert.True(t, c.shouldEvictOldest())
}

func TestFlowCache_AddFlowToCache(t *testing.T) {
	outFlows := make(chan pb.Flow, 10)
	c := NewFlowCache(10*time.Second, 100, outFlows)

	flow := &MockFlow{startTimestamp: time.Now(), key: "flow1"}
	c.addFlowToCache(flow)

	assert.Len(t, c.cache, 1)
	assert.Equal(t, c.queue.Len(), 1)
	assert.Equal(t, flow, c.queue.Front().Value)
}

func TestFlowCache_ResetTimerForNextExpiration(t *testing.T) {
	outFlows := make(chan pb.Flow, 10)
	c := NewFlowCache(10*time.Second, 100, outFlows)

	// Create a test flow and add it to the cache.
	flow := &MockFlow{startTimestamp: time.Now(), key: "flow1"}
	c.addFlowToCache(flow)

	// Create a new timer.
	timer := time.NewTimer(1 * time.Second)
	defer timer.Stop()

	// Expected expiration time.
	expectedTime := flow.StartTimestamp().Add(c.activeTimeout)

	// Reset the timer based on the next flow's expiration.
	c.resetTimerForNextExpiration(timer)

	remainingTime := time.Until(expectedTime)
	actualRemainingTime := time.Until(time.Now().Add(remainingTime))

	// Allow a small margin of error for timing inaccuracies.
	assert.InDelta(t, remainingTime, actualRemainingTime, float64(100*time.Millisecond))
}

func TestFlowCache_EvictOldestFlow(t *testing.T) {
	outFlows := make(chan pb.Flow, 10)
	c := NewFlowCache(10*time.Second, 100, outFlows)

	flow := &MockFlow{startTimestamp: time.Now(), key: "flow1"}
	c.addFlowToCache(flow)

	ctx := context.Background()
	err := c.evictOldestFlow(ctx)
	assert.NoError(t, err)

	assert.Len(t, c.cache, 0)
	assert.Equal(t, 0, c.queue.Len())

	evictedFlow := <-c.outFlows
	assert.Equal(t, flow, evictedFlow)
}

func TestFlowCache_Run(t *testing.T) {
	outFlows := make(chan pb.Flow, 10)
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	c := NewFlowCache(10*time.Second, 100, outFlows)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		time.Sleep(1 * time.Second)
		cancel()
	}()

	// Test termination of Run loop when context is cancelled
	err := c.Run(ctx, logger)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}
