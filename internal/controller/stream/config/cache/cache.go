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

// Access patterns:
// 1. For snapshot ingestion, caller manages locking directly:
//	cache.Mutex.Lock()
//	cache.Reset()
//	cache.InsertLocked(id, obj)
//	cache.NotifyReady()
//	cache.Mutex.Unlock()
//
// 2. For mutations after snapshot, use regular methods which handle locking internally.

// Block on <-cache.Ready() to wait for the first snapshot to complete before reading.
type ConfiguredObjectCache struct {
	// Mutex is exported so callers can hold the lock across multiple operations
	// (e.g., during snapshot ingestion). Use *Locked methods when holding this lock.
	Mutex sync.RWMutex

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

// Ready returns a channel that is closed when the first snapshot is complete.
// Use <-cache.Ready() to block until the cache has consistent data.
func (c *ConfiguredObjectCache) Ready() <-chan struct{} {
	return c.ready
}

// Reset removes all objects from the cache.
// Caller MUST hold Mutex before calling this method.
//
// This method exists because the cache is a singleton with external references
// (e.g., reconciliation loops waiting on Ready()). On stream reconnection,
// the client clears the cache to ingest a fresh snapshot rather than creating
// a new cache instance, which would orphan those references.
func (c *ConfiguredObjectCache) Reset() {
	c.objects = make(map[string]*pb.ConfiguredKubernetesObjectData)
}

// InsertLocked adds or updates an object in the cache when the caller already holds the lock.
// Use during snapshot ingestion after BeginSnapshot() has been called.
func (c *ConfiguredObjectCache) InsertLocked(id string, obj *pb.ConfiguredKubernetesObjectData) {
	c.objects[id] = obj
}

// DeleteLocked removes an object from the cache by ID when the caller already holds the lock.
// Use during snapshot ingestion after BeginSnapshot() has been called.
func (c *ConfiguredObjectCache) DeleteLocked(id string) {
	delete(c.objects, id)
}

// Insert adds or updates an object in the cache.
// This method handles locking internally - use for mutations after snapshot.
func (c *ConfiguredObjectCache) Insert(id string, obj *pb.ConfiguredKubernetesObjectData) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()

	c.objects[id] = obj
}

// Delete removes an object from the cache by ID.
// This method handles locking internally - use for mutations after snapshot.
func (c *ConfiguredObjectCache) Delete(id string) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()

	delete(c.objects, id)
}

// Get retrieves an object by ID. Returns nil if not found.
func (c *ConfiguredObjectCache) Get(id string) *pb.ConfiguredKubernetesObjectData {
	c.Mutex.RLock()
	defer c.Mutex.RUnlock()

	return c.objects[id]
}

// Values returns all objects in the cache, sorted by ID for consistency.
func (c *ConfiguredObjectCache) Values() []*pb.ConfiguredKubernetesObjectData {
	c.Mutex.RLock()
	defer c.Mutex.RUnlock()

	return c.valuesLocked()
}

// valuesLocked returns all objects sorted by ID when the caller already holds the lock.
// Caller MUST hold at least a read lock.
func (c *ConfiguredObjectCache) valuesLocked() []*pb.ConfiguredKubernetesObjectData {
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
	c.Mutex.RLock()
	defer c.Mutex.RUnlock()

	return len(c.objects)
}

// LenLocked returns the number of objects when the caller already holds the lock.
// Caller MUST hold at least a read lock.
func (c *ConfiguredObjectCache) LenLocked() int {
	return len(c.objects)
}

// NotifyReady marks the cache as ready, after a consistent snapshot has been
// successfully inserted into the cache. This method is idempotent.
// The mutex must be held for writes when calling this method.
func (c *ConfiguredObjectCache) NotifyReady() {
	select {
	case <-c.ready:
		// The channel is already closed. Do nothing.
	default:
		close(c.ready)
	}
}
