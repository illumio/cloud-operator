// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cache

import (
	"context"
	"maps"
	"slices"
	"sync"

	"google.golang.org/protobuf/proto"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// ObjectCache is a generic thread-safe cache for storing objects by key.
// Keys use the format "kind/namespace/name".
// It supports atomic replacement for snapshot-based updates.
//
// This cache is generic to support both:
//   - Config cache: stores *pb.ConfiguredKubernetesObjectData from CloudSecure (desired state)
//   - Runtime cache: stores *pb.ConfiguredKubernetesObjectData from Kubernetes (actual state)
//
// Access patterns:
//   - Snapshot: Build a local map, then call ReplaceAll() to atomically swap it in
//   - Mutations: Use Insert() and Delete() which handle locking internally
//   - Reading: Use Get(), Values(), Len() which handle locking internally
//
// Block on <-cache.IsReady() to wait for the first snapshot to complete before reading.
//
// The cache notifies consumers of changes via the resourceChanged channel.
// Insert and Delete send the object key; ReplaceAll sends SnapshotReplaced to indicate
// a full snapshot.
type ObjectCache[T proto.Message] struct {
	mutex sync.RWMutex

	// objects maps object key to its value.
	objects map[string]T

	// ready is closed when the first snapshot is complete.
	ready chan struct{}

	// resourceChanged carries the key of every resource modified.
	// SnapshotReplaced ("*") indicates the entire cache was replaced via a full snapshot.
	// Buffered by one so a snapshot notification can be enqueued without blocking
	// on a consumer that is not yet draining.
	resourceChanged chan string
}

// SnapshotReplaced is the value sent on the resourceChanged channel
// when the entire cache is to be replaced via ReplaceAll.
const SnapshotReplaced = "*"

// NewObjectCache creates a new cache instance.
func NewObjectCache[T proto.Message]() *ObjectCache[T] {
	return &ObjectCache[T]{
		objects: make(map[string]T),
		ready:   make(chan struct{}),
		// Buffered by one so a snapshot swap (ReplaceAll) can enqueue its
		// SnapshotReplaced notification without blocking on a consumer that is
		// not yet draining (e.g. the reconciler still waiting on another cache to
		// become ready). Without the buffer the first snapshot can deadlock the
		// resource stream, which then fails its liveness probe and crash-loops.
		resourceChanged: make(chan string, 1),
	}
}

// ConfiguredObjectCache is the cache type for CloudSecure configured objects.
type ConfiguredObjectCache = ObjectCache[*pb.ConfiguredKubernetesObjectData]

// NewConfiguredObjectCache creates a new cache for configured objects.
func NewConfiguredObjectCache() *ConfiguredObjectCache {
	return NewObjectCache[*pb.ConfiguredKubernetesObjectData]()
}

// Close closes the resourceChanged channel, unblocking any goroutines waiting to send.
func (c *ObjectCache[T]) Close() {
	close(c.resourceChanged)
}

// IsReady returns a channel that is closed when the first snapshot is complete.
// Use <-cache.IsReady() to block until the cache has consistent data.
func (c *ObjectCache[T]) IsReady() <-chan struct{} {
	return c.ready
}

// ResourceChanged returns the channel that emits the key of every resource
// modified. SnapshotReplaced ("*") indicates the entire cache was replaced via a full
// snapshot (ReplaceAll). The cache owns this channel and writes to it on Insert,
// Delete, and ReplaceAll.
func (c *ObjectCache[T]) ResourceChanged() <-chan string {
	return c.resourceChanged
}

// ReplaceAll atomically replaces all objects in the cache with the provided map.
// Use this for snapshot ingestion: build a local map, then call ReplaceAll to
// swap it in. The cache remains consistent until the swap completes.
// Also marks the cache as ready on first call to indicate cache now has valid data.
// Sends SnapshotReplaced on the resourceChanged channel to signal a full snapshot
// replacement. The notification is always delivered: if the single-slot buffer is
// full, ReplaceAll drains the stale value and retries, ensuring SnapshotReplaced
// is enqueued without blocking indefinitely.
func (c *ObjectCache[T]) ReplaceAll(ctx context.Context, objects map[string]T) error {
	c.mutex.Lock()

	c.objects = objects

	// Mark ready (idempotent - safe to call on reconnect)
	select {
	case <-ctx.Done():
		c.mutex.Unlock()

		return ctx.Err()
	case <-c.ready:
		// Already closed
	default:
		close(c.ready)
	}

	c.mutex.Unlock()

	// Notify consumers of the full replacement. SnapshotReplaced supersedes any
	// per-key signal already queued (it means "reconcile everything", which
	// subsumes any single object change). The loop guarantees SnapshotReplaced is
	// delivered without blocking indefinitely: if the single-slot buffer is full,
	// it drains the stale value and retries the send. This decouples snapshot
	// completion from consumer readiness and prevents the startup/reconnect
	// deadlock that would otherwise wedge the resource stream and trip the
	// liveness probe.
Send:
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case c.resourceChanged <- SnapshotReplaced:
			break Send
		case <-c.resourceChanged:
			// Buffer was full; drained a stale queued value, retry the send.
		}
	}

	return nil
}

// Insert adds or updates an object in the cache.
// Sends the object key on the resourceChanged channel only if the object changed.
func (c *ObjectCache[T]) Insert(ctx context.Context, key string, obj T) error {
	c.mutex.Lock()
	old, exists := c.objects[key]
	c.objects[key] = obj
	c.mutex.Unlock()

	if !exists || !proto.Equal(old, obj) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case c.resourceChanged <- key:
		}
	}

	return nil
}

// Delete removes an object from the cache by key.
// Sends the object key on the resourceChanged channel only if the object existed.
func (c *ObjectCache[T]) Delete(ctx context.Context, key string) error {
	c.mutex.Lock()
	_, exists := c.objects[key]
	delete(c.objects, key)
	c.mutex.Unlock()

	if exists {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case c.resourceChanged <- key:
		}
	}

	return nil
}

// Get retrieves an object by key. Returns the zero value if not found.
func (c *ObjectCache[T]) Get(key string) T {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return c.objects[key]
}

// Keys returns all object keys in the cache.
func (c *ObjectCache[T]) Keys() []string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return slices.Collect(maps.Keys(c.objects))
}

// Values returns all objects in the cache, sorted by key for consistency.
func (c *ObjectCache[T]) Values() []T {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if len(c.objects) == 0 {
		return nil
	}

	result := make([]T, 0, len(c.objects))
	for _, key := range slices.Sorted(maps.Keys(c.objects)) {
		result = append(result, c.objects[key])
	}

	return result
}

// Len returns the number of objects in the cache.
func (c *ObjectCache[T]) Len() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return len(c.objects)
}
