// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cache

import (
	"context"
	"strconv"
	"testing"
	"testing/synctest"
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

// newCiliumFlow returns a Hubble event for TCP client:42138 -> api:8080 as
// observed on the node running both pods. Hubble sets endpoint IDs only for
// local pods, so events share a flow key only when observed on the same node.
func newCiliumFlow(verdict pb.Verdict, timestamp time.Time) *pb.CiliumFlow {
	return &pb.CiliumFlow{
		Time:    timestamppb.New(timestamp),
		Verdict: verdict,
		Layer3: &pb.IP{
			Source:      "10.1.54.107",
			Destination: "10.1.48.226",
			IpVersion:   pb.IPVersion_IP_VERSION_IPV4,
		},
		Layer4: &pb.Layer4{
			Protocol: &pb.Layer4_Tcp{Tcp: &pb.TCP{SourcePort: 42138, DestinationPort: 8080}},
		},
		SourceEndpoint:      &pb.Endpoint{Uid: 1201, PodName: "client"},
		DestinationEndpoint: &pb.Endpoint{Uid: 3517, PodName: "api"},
	}
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

	c.cache[expiredFlow.Key()] = c.queue.PushBack(expiredFlow)
	c.cache[activeFlow.Key()] = c.queue.PushBack(activeFlow)

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
	c.addFlowToCache(flow)

	assert.True(t, c.shouldSkipFlow(flow))
}

func TestFlowCache_ShouldSkipFlow_Verdicts(t *testing.T) {
	tests := []struct {
		name     string
		cached   pb.Verdict
		incoming pb.Verdict
		wantSkip bool
	}{
		{name: "dropped replaces forwarded", cached: pb.Verdict_VERDICT_FORWARDED, incoming: pb.Verdict_VERDICT_DROPPED},
		{name: "audit replaces forwarded", cached: pb.Verdict_VERDICT_FORWARDED, incoming: pb.Verdict_VERDICT_AUDIT},
		{name: "dropped replaces audit", cached: pb.Verdict_VERDICT_AUDIT, incoming: pb.Verdict_VERDICT_DROPPED},
		{name: "forwarded after dropped is skipped", cached: pb.Verdict_VERDICT_DROPPED, incoming: pb.Verdict_VERDICT_FORWARDED, wantSkip: true},
		{name: "audit after dropped is skipped", cached: pb.Verdict_VERDICT_DROPPED, incoming: pb.Verdict_VERDICT_AUDIT, wantSkip: true},
		{name: "forwarded after audit is skipped", cached: pb.Verdict_VERDICT_AUDIT, incoming: pb.Verdict_VERDICT_FORWARDED, wantSkip: true},
		{name: "same verdict is skipped", cached: pb.Verdict_VERDICT_DROPPED, incoming: pb.Verdict_VERDICT_DROPPED, wantSkip: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewFlowCache(10*time.Second, 100, make(chan pb.Flow, 10))
			now := time.Now()

			c.addFlowToCache(newCiliumFlow(tt.cached, now))

			assert.Equal(t, tt.wantSkip, c.shouldSkipFlow(newCiliumFlow(tt.incoming, now)))
		})
	}
}

// TestFlowCache_Run_DroppedReplacesForwarded replays the Hubble events for one
// connection denied by ingress policy, with both pods on one node: to-stack
// FORWARDED on the client's egress, then policy-verdict and drop events (both
// DROPPED) for the SYN and for its retransmission one second later.
func TestFlowCache_Run_DroppedReplacesForwarded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const activeTimeout = 20 * time.Second

		outFlows := make(chan pb.Flow, 10)
		logger, _ := zap.NewDevelopment()
		c := NewFlowCache(activeTimeout, 100, outFlows)

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)

		go func() {
			done <- c.Run(ctx, logger)
		}()

		policyVerdict := newCiliumFlow(pb.Verdict_VERDICT_DROPPED, time.Now())
		for _, flow := range []pb.Flow{
			newCiliumFlow(pb.Verdict_VERDICT_FORWARDED, time.Now()),
			policyVerdict,
			newCiliumFlow(pb.Verdict_VERDICT_DROPPED, time.Now()),
		} {
			require.NoError(t, c.CacheFlow(ctx, flow))
		}

		time.Sleep(time.Second)

		for range 2 {
			require.NoError(t, c.CacheFlow(ctx, newCiliumFlow(pb.Verdict_VERDICT_DROPPED, time.Now())))
		}

		time.Sleep(activeTimeout)
		synctest.Wait()

		require.Len(t, outFlows, 1)
		assert.Same(t, policyVerdict, <-outFlows)

		cancel()
		require.ErrorIs(t, <-done, context.Canceled)
	})
}

func TestFlowCache_ShouldEvictOldest(t *testing.T) {
	outFlows := make(chan pb.Flow, 10)
	c := NewFlowCache(10*time.Second, 100, outFlows)

	for i := range 100 {
		flow := &MockFlow{startTimestamp: time.Now(), key: "flow" + strconv.Itoa(i)}
		c.cache[flow.Key()] = c.queue.PushBack(flow)
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
