// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cache

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

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
	assert.Equal(t, c.OutFlows, outFlows)
	assert.Empty(t, c.cache)
	assert.Equal(t, 0, c.queue.Len())
}

func TestFlowCache_Close(t *testing.T) {
	outFlows := make(chan pb.Flow)
	c := NewFlowCache(10*time.Second, 100, outFlows)

	err := c.Close()
	require.NoError(t, err)

	_, ok := <-c.inFlows
	assert.False(t, ok, "inFlows channel should be closed")

	_, ok = <-c.OutFlows
	assert.False(t, ok, "outFlows channel should be closed")
}

func TestFlowCache_CacheFlow(t *testing.T) {
	outFlows := make(chan pb.Flow, 1)
	c := NewFlowCache(10*time.Second, 100, outFlows)

	ctx := context.Background()
	flow := &MockFlow{startTimestamp: time.Now(), key: "flow1"}

	err := c.CacheFlow(ctx, flow)
	require.NoError(t, err)

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
	logger, _ := zap.NewDevelopment()
	c.evictExpiredFlows(ctx, logger)

	assert.Len(t, c.cache, 1)
	assert.Equal(t, 1, c.queue.Len())
	assert.Equal(t, activeFlow, c.queue.Front().Value)

	evictedFlow := <-c.OutFlows
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

	for i := range 100 {
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
	assert.Equal(t, 1, c.queue.Len())
	assert.Equal(t, flow, c.queue.Front().Value)
}

func TestFlowCache_AddFlowToCache_SortedOrder(t *testing.T) {
	outFlows := make(chan pb.Flow, 10)
	c := NewFlowCache(10*time.Second, 100, outFlows)

	now := time.Now()
	flow1 := &MockFlow{startTimestamp: now.Add(-10 * time.Second), key: "flow1"} // oldest
	flow2 := &MockFlow{startTimestamp: now.Add(-5 * time.Second), key: "flow2"}  // middle
	flow3 := &MockFlow{startTimestamp: now, key: "flow3"}                        // newest

	// Add out of order: newest, oldest, middle
	c.addFlowToCache(flow3)
	c.addFlowToCache(flow1)
	c.addFlowToCache(flow2)

	assert.Len(t, c.cache, 3)
	assert.Equal(t, 3, c.queue.Len())

	// Verify sorted order: oldest at front, newest at back
	elem := c.queue.Front()
	assert.Equal(t, flow1, elem.Value, "oldest flow should be at front")

	elem = elem.Next()
	assert.Equal(t, flow2, elem.Value, "middle flow should be second")

	elem = elem.Next()
	assert.Equal(t, flow3, elem.Value, "newest flow should be at back")
}

func TestFlowCache_EvictOldestFlow(t *testing.T) {
	outFlows := make(chan pb.Flow, 10)
	c := NewFlowCache(10*time.Second, 100, outFlows)

	flow := &MockFlow{startTimestamp: time.Now(), key: "flow1"}
	c.addFlowToCache(flow)

	ctx := context.Background()
	logger, _ := zap.NewDevelopment()
	err := c.evictOldestFlow(ctx, logger)
	require.NoError(t, err)

	assert.Empty(t, c.cache)
	assert.Equal(t, 0, c.queue.Len())

	evictedFlow := <-c.OutFlows
	assert.Equal(t, flow, evictedFlow)
}

// newFiveTupleFlow returns a flow for the same TCP 5-tuple with the given verdict.
func newFiveTupleFlow(ts time.Time, verdict pb.Verdict) *pb.FiveTupleFlow {
	return &pb.FiveTupleFlow{
		Layer3:  &pb.IP{Source: "10.0.1.28", Destination: "10.0.1.132", IpVersion: pb.IPVersion_IP_VERSION_IPV4},
		Layer4:  &pb.Layer4{Protocol: &pb.Layer4_Tcp{Tcp: &pb.TCP{SourcePort: 55484, DestinationPort: 80}}},
		Ts:      &pb.FiveTupleFlow_Timestamp{Timestamp: timestamppb.New(ts)},
		Verdict: verdict,
	}
}

func TestFlowCache_FiveTupleFlowVerdictsNotDeduplicated(t *testing.T) {
	outFlows := make(chan pb.Flow, 10)
	c := NewFlowCache(10*time.Second, 100, outFlows)

	ts := time.Now().Add(-time.Minute)
	accept := newFiveTupleFlow(ts, pb.Verdict_VERDICT_FORWARDED)
	deny := newFiveTupleFlow(ts, pb.Verdict_VERDICT_DROPPED)
	acceptAgain := newFiveTupleFlow(ts, pb.Verdict_VERDICT_FORWARDED)

	// Mirror Run's inFlows handling: skip duplicates, cache the rest.
	for _, flow := range []pb.Flow{accept, deny, acceptAgain} {
		if !c.shouldSkipFlow(flow) {
			c.addFlowToCache(flow)
		}
	}

	c.evictExpiredFlows(context.Background(), zap.NewNop())

	require.Len(t, outFlows, 2, "ACCEPT and DENY for the same 5-tuple must both be exported")
	assert.Same(t, accept, <-outFlows)
	assert.Same(t, deny, <-outFlows)
}

func TestFlowCache_Run(t *testing.T) {
	outFlows := make(chan pb.Flow, 10)
	logger, _ := zap.NewDevelopment()

	c := NewFlowCache(10*time.Second, 100, outFlows)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		time.Sleep(1 * time.Second)
		cancel()
	}()

	err := c.Run(ctx, logger)
	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}
