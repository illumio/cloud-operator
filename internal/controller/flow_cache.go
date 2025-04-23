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

// cacheManagerIndefinitely manages the flow cache by evicting expired flows based on the active timeout,
// processing new flows, and resetting the timer for the next expiration.
func (c *FlowCache) Run(ctx context.Context, logger *zap.Logger) error {
	// Create a new timer to trigger every activeTimeout duration.
	timer := time.NewTimer(c.activeTimeout)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done(): // If the context is canceled, exit the loop and return the error.
			return ctx.Err()

		case <-timer.C:
			now := time.Now().UTC()
			activeTimeoutCutoff := now.Sub(time.Now().Add(c.activeTimeout)) // Determine the cutoff time for expired flows.

			// Iterate through the flow cache queue to evict expired flows.
			for c.queue.Len() > 0 {
				frontElem := c.queue.Front()
				flow := frontElem.Value.(pb.Flow)
				flowTimestamp := flow.StartTimestamp()
				flowKey := flow.Key()
				// If the flow is not expired (timestamp is newer than the cutoff), stop evicting.
				if flowTimestamp.After(time.Now().Add(activeTimeoutCutoff)) {
					break
				}

				// Evict the expired flow by removing it from the cache and queue.
				c.queue.Remove(frontElem)
				delete(c.cache, flowKey)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case c.outFlows <- flow:
				}
			}

			// Reset the timer to expire based on the next flow's timestamp.
			if front := c.queue.Front(); front != nil {
				resetTimer(timer, front.Value.(pb.Flow).StartTimestamp(), c.activeTimeout)
			}
		case flow := <-c.inFlows:
			// If the flow already exists in the cache, skip adding it again.
			if _, found := c.cache[flow.Key()]; found {
				continue
			}

			// If the cache is full, evict the oldest flow to make room for the new flow.
			if len(c.cache) >= c.maxFlows {
				oldestFlow := c.queue.Front()
				c.queue.Remove(oldestFlow)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case c.outFlows <- oldestFlow.Value.(pb.Flow):
				}
			}

			// Add the new flow to the cache and queue.
			c.queue.PushBack(flow)
			c.cache[flow.Key()] = flow
			oldestFlow := c.queue.Front().Value.(pb.Flow)
			// Reset the timer based on the oldest flow's timestamp (queue is ordered so the front element is oldest).
			resetTimer(timer, oldestFlow.StartTimestamp(), c.activeTimeout)
		}
	}
}

func resetTimer(timer *time.Timer, timestamp time.Time, timeout time.Duration) {
	delay := time.Until(timestamp.Add(timeout))
	timer.Reset(delay)
}
