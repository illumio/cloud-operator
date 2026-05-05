// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cache

import (
	"maps"
	"slices"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// ObjectCache is a generic thread-safe cache for storing objects by ID.
// It supports atomic replacement for snapshot-based updates.
//
// This cache is generic to support both:
//   - Config cache: stores *pb.ConfiguredKubernetesObjectData from CloudSecure (desired state)
//   - Runtime cache: stores *unstructured.Unstructured from Kubernetes (actual state)
//
// Access patterns:
//   - Snapshot: Build a local map, then call ReplaceAll() to atomically swap it in
//   - Mutations: Use Insert() and Delete() which handle locking internally
//   - Reading: Use Get(), Values(), Len() which handle locking internally
//
// Block on <-cache.IsReady() to wait for the first snapshot to complete before reading.
type ObjectCache[T any] struct {
	mutex sync.RWMutex

	// objects maps object ID to its value.
	objects map[string]T

	// ready is closed when the first snapshot is complete.
	ready chan struct{}

	// onChange is called when the cache changes (after ready).
	onChange func()
}

// NewObjectCache creates a new cache instance.
func NewObjectCache[T any]() *ObjectCache[T] {
	return &ObjectCache[T]{
		objects: make(map[string]T),
		ready:   make(chan struct{}),
	}
}

// ConfiguredObjectCache is the cache type for CloudSecure configured objects.
type ConfiguredObjectCache = ObjectCache[*pb.ConfiguredKubernetesObjectData]

// RuntimeCache is the cache type for runtime Kubernetes objects.
// It stores unstructured objects because reconciliation requires Kubernetes metadata
// fields not present in the proto, such as resourceVersion and ownerReferences.
type RuntimeCache = ObjectCache[*unstructured.Unstructured]

// NewConfiguredObjectCache creates a new cache for configured objects.
func NewConfiguredObjectCache() *ConfiguredObjectCache {
	return NewObjectCache[*pb.ConfiguredKubernetesObjectData]()
}

// NewRuntimeCache creates a new cache for runtime objects.
func NewRuntimeCache() *RuntimeCache {
	return NewObjectCache[*unstructured.Unstructured]()
}

// IsReady returns a channel that is closed when the first snapshot is complete.
// Use <-cache.IsReady() to block until the cache has consistent data.
func (c *ObjectCache[T]) IsReady() <-chan struct{} {
	return c.ready
}

// OnChange registers a callback that is called when the cache changes.
// The callback is only called after the cache is ready (first snapshot complete).
// Only one callback can be registered; subsequent calls replace the previous one.
func (c *ObjectCache[T]) OnChange(fn func()) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.onChange = fn
}

// ReplaceAll atomically replaces all objects in the cache with the provided map.
// Use this for snapshot ingestion: build a local map, then call ReplaceAll to
// swap it in. The cache remains consistent until the swap completes.
// Also marks the cache as ready on first call to indicate cache now has valid data.
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

	onChange := c.onChange
	c.mutex.Unlock()

	// Notify after releasing lock
	if onChange != nil {
		onChange()
	}
}

// Insert adds or updates an object in the cache.
// Use for mutations after snapshot is complete.
func (c *ObjectCache[T]) Insert(id string, obj T) {
	c.mutex.Lock()

	c.objects[id] = obj
	onChange := c.onChange

	c.mutex.Unlock()

	// Notify after releasing lock
	if onChange != nil {
		onChange()
	}
}

// Delete removes an object from the cache by ID.
// Use for mutations after snapshot is complete.
func (c *ObjectCache[T]) Delete(id string) {
	c.mutex.Lock()

	delete(c.objects, id)
	onChange := c.onChange

	c.mutex.Unlock()

	// Notify after releasing lock
	if onChange != nil {
		onChange()
	}
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
