// Copyright 2025 Illumio, Inc. All Rights Reserved.

package controller

import (
	"container/list"
	"context"
	"io"
	"time"

	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// FlowCache caches flows to be exported.
// Evicts flows from the cache to be send to the collector for any of the following reasons:
// - lack of resources: the cache has reached the maximum capacity configured in maxFlows
// - active timeout: the flow has been cache longer than the configured activeTimeout
// See https://www.rfc-editor.org/rfc/rfc5102.html#section-5.11.3 for the definition of those reasons.
type FlowCache struct {
	// cache is the set of aggregated flows indexed by their flow keys.
	cache map[any]pb.Flow
	// queue is the list of cached flows contained in cache, ordered by Timestamp.
	queue *list.List
	// activeTimeout is the maximum duration any flow remains in the cache.
	activeTimeout time.Duration
	// maxFlows is the maximum number of flows cached at any time.
	maxFlows int
	// inFlows contains flows to be aggregated and cached.
	inFlows chan pb.Flow
	// outFlows contains flows that have been evicted from the cache.
	outFlows chan pb.Flow
}

var _ io.Closer = &FlowCache{}

func NewFlowCache(
	activeTimeout time.Duration,
	maxFlows int,
	outFlows chan pb.Flow,
) *FlowCache {
	return &FlowCache{
		cache:         make(map[any]pb.Flow, maxFlows),
		queue:         list.New(),
		activeTimeout: activeTimeout,
		maxFlows:      maxFlows,
		inFlows:       make(chan pb.Flow, 1),
		outFlows:      outFlows,
	}
}

// Close closes this flow cache's channels.
// This method must be called exactly once on every FlowCache after use.
func (c *FlowCache) Close() error {
	close(c.inFlows)
	close(c.outFlows)
	return nil
}

// CacheFlow aggregates and caches the given flow.
func (c *FlowCache) CacheFlow(ctx context.Context, flow pb.Flow) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case c.inFlows <- flow:
		return nil
	}
}

// Run manages the flow cache by evicting expired flows based on the active timeout,
// processing new flows, and resetting the timer for the next expiration.
func (c *FlowCache) Run(ctx context.Context, logger *zap.Logger) error {
	timer := time.NewTimer(c.activeTimeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-timer.C:
			c.evictExpiredFlows(ctx)

			// Reset timer based on the next soon-to-expire flow
			if front := c.queue.Front(); front != nil {
				resetTimer(timer, front.Value.(pb.Flow).StartTimestamp(), c.activeTimeout)
			}

		case flow := <-c.inFlows:
			if c.shouldSkipFlow(flow) {
				continue
			}

			if c.shouldEvictOldest() {
				if err := c.evictOldestFlow(ctx); err != nil {
					return err
				}
			}

			c.addFlowToCache(flow)
			c.resetTimerForNextExpiration(timer)
		}
	}
}

// evictExpiredFlows removes all flows older than the active timeout from the cache and queue.
func (c *FlowCache) evictExpiredFlows(ctx context.Context) {
	now := time.Now().UTC()
	cutoff := now.Add(-c.activeTimeout)

	for c.queue.Len() > 0 {
		frontElem := c.queue.Front()
		flow := frontElem.Value.(pb.Flow)
		if flow.StartTimestamp().After(cutoff) {
			break
		}
		c.queue.Remove(frontElem)
		delete(c.cache, flow.Key())
		select {
		case <-ctx.Done():
			return
		case c.outFlows <- flow:
		}
	}
}

// shouldSkipFlow determines if a flow is already cached and logs if skipped.
func (c *FlowCache) shouldSkipFlow(flow pb.Flow) bool {
	_, alreadyCached := c.cache[flow.Key()]
	return alreadyCached
}

// shouldEvictOldest checks if cache size has reached its limit.
func (c *FlowCache) shouldEvictOldest() bool {
	return len(c.cache) >= c.maxFlows
}

// evictOldestFlow removes the oldest flow from cache and sends it out.
func (c *FlowCache) evictOldestFlow(ctx context.Context) error {
	oldest := c.queue.Front()
	if oldest == nil {
		return nil
	}
	c.queue.Remove(oldest)
	flow := oldest.Value.(pb.Flow)
	delete(c.cache, flow.Key())
	select {
	case <-ctx.Done():
		return ctx.Err()
	case c.outFlows <- flow:
	}
	return nil
}

// addFlowToCache adds a new flow to both the map and queue.
func (c *FlowCache) addFlowToCache(flow pb.Flow) {
	c.queue.PushBack(flow)
	c.cache[flow.Key()] = flow
}

// resetTimerForNextExpiration resets the eviction timer based on the oldest flow's expiration time.
func (c *FlowCache) resetTimerForNextExpiration(timer *time.Timer) {
	if oldest := c.queue.Front(); oldest != nil {
		resetTimer(timer, oldest.Value.(pb.Flow).StartTimestamp(), c.activeTimeout)
	}
}

// resetTimer recalculates and sets the timer delay for the next flow to expire.
func resetTimer(timer *time.Timer, timestamp time.Time, timeout time.Duration) {
	delay := time.Until(timestamp.Add(timeout))
	timer.Reset(delay)
}
