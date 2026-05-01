// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cache

import (
	"maps"
	"slices"
	"sync"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// ConfiguredObjectCache stores configured Kubernetes objects received from CloudSecure.
// It tracks the desired state that CloudSecure wants in the cluster.
//
// Access patterns:
//   - Snapshot: Build a local map, then call ReplaceAll() to atomically swap it in
//   - Mutations: Use Insert() and Delete() which handle locking internally
//   - Reading (for the reconciler): Use Get(), Values(), Len() which handle locking internally
//
// Block on <-cache.IsReady() to wait for the first snapshot to complete before reading.
type ConfiguredObjectCache struct {
	mutex sync.RWMutex

	// objects maps object ID to its ConfiguredKubernetesObjectData.
	objects map[string]*pb.ConfiguredKubernetesObjectData

	// ready is closed when the first snapshot is complete.
	ready chan struct{}
}

// NewConfiguredObjectCache creates a new cache instance.
func NewConfiguredObjectCache() *ConfiguredObjectCache {
	return &ConfiguredObjectCache{
		objects: make(map[string]*pb.ConfiguredKubernetesObjectData),
		ready:   make(chan struct{}),
	}
}

// IsReady returns a channel that is closed when the first snapshot is complete.
// Use <-cache.IsReady() to block until the cache has consistent data.
func (c *ConfiguredObjectCache) IsReady() <-chan struct{} {
	return c.ready
}

// ReplaceAll atomically replaces all objects in the cache with the provided map.
// Use this for snapshot ingestion: build a local map, then call ReplaceAll to
// swap it in. The cache remains consistent until the swap completes.
// Also marks the cache as ready on first call to indicate cache now has valid data.
func (c *ConfiguredObjectCache) ReplaceAll(objects map[string]*pb.ConfiguredKubernetesObjectData) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.objects = objects

	// Mark ready (idempotent - safe to call on reconnect)
	select {
	case <-c.ready:
		// Already closed
	default:
		close(c.ready)
	}
}

// Insert adds or updates an object in the cache.
// Use for mutations after snapshot is complete.
func (c *ConfiguredObjectCache) Insert(id string, obj *pb.ConfiguredKubernetesObjectData) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.objects[id] = obj
}

// Delete removes an object from the cache by ID.
// Use for mutations after snapshot is complete.
func (c *ConfiguredObjectCache) Delete(id string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	delete(c.objects, id)
}

// Get retrieves an object by ID. Returns nil if not found.
func (c *ConfiguredObjectCache) Get(id string) *pb.ConfiguredKubernetesObjectData {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return c.objects[id]
}

// Values returns all objects in the cache, sorted by ID for consistency.
func (c *ConfiguredObjectCache) Values() []*pb.ConfiguredKubernetesObjectData {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if len(c.objects) == 0 {
		return nil
	}

	result := make([]*pb.ConfiguredKubernetesObjectData, 0, len(c.objects))
	for _, key := range slices.Sorted(maps.Keys(c.objects)) {
		result = append(result, c.objects[key])
	}

	return result
}

// Len returns the number of objects in the cache.
func (c *ConfiguredObjectCache) Len() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return len(c.objects)
}
