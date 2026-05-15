// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cache

import (
	"maps"
	"slices"
	"sync"

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
// The cache notifies consumers of changes via an unbuffered resourceChanged channel.
// Insert and Delete send the object ID; ReplaceAll sends SnapshotReplaced to indicate
// a full snapshot.
type ObjectCache[T any] struct {
	mutex sync.RWMutex

	// objects maps object ID to its value.
	objects map[string]T

	// ready is closed when the first snapshot is complete.
	ready chan struct{}

	// resourceChanged carries the ID of every resource modified.
	// SnapshotReplaced ("*") indicates the entire cache was replaced via a full snapshot.
	// Unbuffered: the sender blocks until the consumer reads.
	resourceChanged chan string
}

// SnapshotReplaced is the value sent on the resourceChanged channel
// when the entire cache is to be replaced via ReplaceAll.
const SnapshotReplaced = "*"

// NewObjectCache creates a new cache instance.
func NewObjectCache[T any]() *ObjectCache[T] {
	return &ObjectCache[T]{
		objects:         make(map[string]T),
		ready:           make(chan struct{}),
		resourceChanged: make(chan string),
	}
}

// ConfiguredObjectCache is the cache type for CloudSecure configured objects.
type ConfiguredObjectCache = ObjectCache[*pb.ConfiguredKubernetesObjectData]

// NewConfiguredObjectCache creates a new cache for configured objects.
func NewConfiguredObjectCache() *ConfiguredObjectCache {
	return NewObjectCache[*pb.ConfiguredKubernetesObjectData]()
}

// RuntimeCache is the cache type for runtime Kubernetes objects.
// It stores the same proto type as the config cache, enabling symmetric comparison
// using proto.Equal so the reconciler only applies when there's an actual diff.
type RuntimeCache = ObjectCache[*pb.ConfiguredKubernetesObjectData]

// NewRuntimeCache creates a new cache for runtime objects.
func NewRuntimeCache() *RuntimeCache {
	return NewObjectCache[*pb.ConfiguredKubernetesObjectData]()
}

// IsReady returns a channel that is closed when the first snapshot is complete.
// Use <-cache.IsReady() to block until the cache has consistent data.
func (c *ObjectCache[T]) IsReady() <-chan struct{} {
	return c.ready
}

// ResourceChanged returns an unbuffered channel that emits the ID of every resource
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
func (c *ObjectCache[T]) ReplaceAll(objects map[string]T) {
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

	c.resourceChanged <- SnapshotReplaced
}

// Insert adds or updates an object in the cache.
// Sends the object ID on the resourceChanged channel.
func (c *ObjectCache[T]) Insert(id string, obj T) {
	c.mutex.Lock()
	c.objects[id] = obj
	c.mutex.Unlock()

	c.resourceChanged <- id
}

// Delete removes an object from the cache by ID.
// Sends the object ID on the resourceChanged channel.
func (c *ObjectCache[T]) Delete(id string) {
	c.mutex.Lock()
	delete(c.objects, id)
	c.mutex.Unlock()

	c.resourceChanged <- id
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
