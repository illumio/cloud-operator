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

// ObjectCache is a generic thread-safe cache for storing objects by ID.
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
// Insert and Delete send the object ID; ReplaceAll sends SnapshotReplaced to indicate
// a full snapshot.
type ObjectCache[T proto.Message] struct {
	mutex sync.RWMutex

	// objects maps object ID to its value.
	objects map[string]T

	// ready is closed when the first snapshot is complete.
	ready chan struct{}

	// resourceChanged carries the ID of every resource modified.
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

// ResourceChanged returns the channel that emits the ID of every resource
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
// Sends SnapshotReplaced on the resourceChanged channel to signal a full snapshot replacement.
func (c *ObjectCache[T]) ReplaceAll(ctx context.Context, objects map[string]T) error {
	c.mutex.Lock()

	c.objects = objects

	// Mark ready (idempotent - safe to call on reconnect)
	select {
	case <-c.ready:
		// Already closed
	default:
		close(c.ready)
	}

	c.mutex.Unlock()

	// Honor cancellation deterministically before attempting to notify, so a
	// cancelled snapshot is never reported as successful.
	if err := ctx.Err(); err != nil {
		return err
	}

	// Notify consumers of the full replacement without blocking the caller.
	// SnapshotReplaced is idempotent ("the whole cache was replaced, reconcile
	// everything"), so the send is buffered by one and coalescing: if a prior
	// SnapshotReplaced is still queued because the consumer is not draining yet
	// (e.g. the reconciler is still waiting on the other cache to become ready,
	// including across stream reconnects that call ReplaceAll again), dropping
	// this one is correct and must not block. This fully decouples snapshot
	// completion from consumer readiness and prevents a startup/reconnect
	// deadlock that would otherwise wedge the resource stream and trip the
	// liveness probe.
	//
	// The ctx.Done() case reports cancellation deterministically when it is
	// already observable at the notification point, without ever blocking the
	// caller (the default keeps the send non-blocking).
	select {
	case c.resourceChanged <- SnapshotReplaced:
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return nil
}

// Insert adds or updates an object in the cache.
// Sends the object ID on the resourceChanged channel only if the object changed.
func (c *ObjectCache[T]) Insert(ctx context.Context, id string, obj T) error {
	c.mutex.Lock()
	old, exists := c.objects[id]
	c.objects[id] = obj
	c.mutex.Unlock()

	if !exists || !proto.Equal(old, obj) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case c.resourceChanged <- id:
		}
	}

	return nil
}

// Delete removes an object from the cache by ID.
// Sends the object ID on the resourceChanged channel only if the object existed.
func (c *ObjectCache[T]) Delete(ctx context.Context, id string) error {
	c.mutex.Lock()
	_, exists := c.objects[id]
	delete(c.objects, id)
	c.mutex.Unlock()

	if exists {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case c.resourceChanged <- id:
		}
	}

	return nil
}

// Get retrieves an object by ID. Returns the zero value if not found.
func (c *ObjectCache[T]) Get(id string) T {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return c.objects[id]
}

// Values returns all objects in the cache, sorted by ID for consistency.
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
