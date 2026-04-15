// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cache

import (
	"container/list"
	"context"
	"io"
	"time"

	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// FlowCache caches flows to be exported.
// Evicts flows from the cache for:
// - lack of resources: cache has reached maxFlows capacity
// - active timeout: flow has been cached longer than activeTimeout.
type FlowCache struct {
	cache         map[any]pb.Flow
	queue         *list.List
	activeTimeout time.Duration
	maxFlows      int
	inFlows       chan pb.Flow
	OutFlows      chan pb.Flow
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
		OutFlows:      outFlows,
	}
}

// Close closes this flow cache's channels.
func (c *FlowCache) Close() error {
	close(c.inFlows)
	close(c.OutFlows)

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

// Run manages the flow cache by evicting expired flows and processing new flows.
func (c *FlowCache) Run(ctx context.Context, logger *zap.Logger) error {
	timer := time.NewTimer(c.activeTimeout)
	defer timer.Stop()

	logger.Debug("Flow cache started",
		zap.Duration("active_timeout", c.activeTimeout),
		zap.Int("max_size", c.maxFlows),
	)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-timer.C:
			c.evictExpiredFlows(ctx, logger)

			if front := c.queue.Front(); front != nil {
				flow, ok := front.Value.(pb.Flow)
				if !ok {
					logger.Fatal("Failed to convert cache entry to Flow")
				}

				resetTimer(timer, flow.StartTimestamp(), c.activeTimeout)
			}

		case flow := <-c.inFlows:
			if c.shouldSkipFlow(flow) {
				continue
			}

			if c.shouldEvictOldest() {
				if err := c.evictOldestFlow(ctx, logger); err != nil {
					return err
				}
			}

			c.addFlowToCache(flow)
			c.resetTimerForNextExpiration(timer, logger)
		}
	}
}

func (c *FlowCache) evictExpiredFlows(ctx context.Context, logger *zap.Logger) {
	now := time.Now().UTC()
	cutoff := now.Add(-c.activeTimeout)
	evictedCount := 0

	for c.queue.Len() > 0 {
		frontElem := c.queue.Front()

		flow, ok := frontElem.Value.(pb.Flow)
		if !ok {
			logger.Fatal("Failed to convert cache entry to Flow")
		}

		if flow.StartTimestamp().After(cutoff) {
			break
		}

		c.queue.Remove(frontElem)
		delete(c.cache, flow.Key())

		evictedCount++

		select {
		case <-ctx.Done():
			return
		case c.OutFlows <- flow:
		}
	}

	if evictedCount > 0 {
		logger.Debug("Evicted expired flows",
			zap.Int("evicted_count", evictedCount),
			zap.Int("cache_size", len(c.cache)),
		)
	}
}

func (c *FlowCache) shouldSkipFlow(flow pb.Flow) bool {
	_, alreadyCached := c.cache[flow.Key()]

	return alreadyCached
}

func (c *FlowCache) shouldEvictOldest() bool {
	return len(c.cache) >= c.maxFlows
}

func (c *FlowCache) evictOldestFlow(ctx context.Context, logger *zap.Logger) error {
	oldest := c.queue.Front()
	if oldest == nil {
		return nil
	}

	c.queue.Remove(oldest)

	flow, ok := oldest.Value.(pb.Flow)
	if !ok {
		logger.Fatal("Failed to convert cache entry to Flow")
	}

	delete(c.cache, flow.Key())

	logger.Debug("Evicted oldest flow due to capacity",
		zap.Int("cache_size", len(c.cache)),
		zap.Int("max_size", c.maxFlows),
	)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case c.OutFlows <- flow:
	}

	return nil
}

func (c *FlowCache) addFlowToCache(flow pb.Flow) {
	c.queue.PushBack(flow)
	c.cache[flow.Key()] = flow
}

func (c *FlowCache) resetTimerForNextExpiration(timer *time.Timer, logger *zap.Logger) {
	if oldest := c.queue.Front(); oldest != nil {
		flow, ok := oldest.Value.(pb.Flow)
		if !ok {
			logger.Fatal("Failed to convert cache entry to Flow")
		}

		resetTimer(timer, flow.StartTimestamp(), c.activeTimeout)
	}
}

func resetTimer(timer *time.Timer, timestamp time.Time, timeout time.Duration) {
	delay := time.Until(timestamp.Add(timeout))
	timer.Reset(delay)
}
