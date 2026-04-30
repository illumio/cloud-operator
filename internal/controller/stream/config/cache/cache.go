// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cache

import (
	"sort"
	"sync"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// ConfiguredObjectCache stores configured Kubernetes objects received from CloudSecure.
// It tracks the desired state that CloudSecure wants in the cluster.
//
// The cache supports two access patterns:
//  1. During snapshot ingestion: caller holds Mutex for entire snapshot, uses *Unlocked methods
//  2. During mutations: caller uses regular methods which handle locking internally
//
// Use Ready() to wait for the first snapshot to complete before reading.
type ConfiguredObjectCache struct {
	// Mutex is exported so callers can hold the lock across multiple operations
	// (e.g., during snapshot ingestion). Use *Unlocked methods when holding this lock.
	Mutex sync.RWMutex

	// Maps object's ID as the key to its ConfiguredKubernetesObjectData as the value
	objects map[string]*pb.ConfiguredKubernetesObjectData
	ready   chan struct{} // Closed when snapshot complete - also serves as boolean flag
}

// NewConfiguredObjectCache creates a new cache instance.
func NewConfiguredObjectCache() *ConfiguredObjectCache {
	return &ConfiguredObjectCache{
		objects: make(map[string]*pb.ConfiguredKubernetesObjectData),
		ready:   make(chan struct{}),
	}
}

// Ready returns a channel that is closed when the first snapshot is complete.
// Use this to block until the cache has consistent data.
func (c *ConfiguredObjectCache) Ready() <-chan struct{} {
	return c.ready
}

// BeginSnapshot prepares the cache for a new snapshot.
// It acquires the write lock and clears the cache.
// The lock is held until EndSnapshot is called.
// This blocks all readers until the snapshot is complete.
func (c *ConfiguredObjectCache) BeginSnapshot() {
	c.Mutex.Lock()
	c.objects = make(map[string]*pb.ConfiguredKubernetesObjectData)
}

// EndSnapshot completes the snapshot and releases the lock.
// It also signals ready (idempotent - only first call closes the channel).
func (c *ConfiguredObjectCache) EndSnapshot() {
	// Close ready channel to signal first snapshot complete (idempotent)
	select {
	case <-c.ready:
		// Already closed - do nothing
	default:
		close(c.ready)
	}

	c.Mutex.Unlock()
}

// InsertUnlocked adds or updates an object in the cache, when you already hold the lock.
// Caller MUST hold Mutex before calling this method.
func (c *ConfiguredObjectCache) InsertUnlocked(id string, obj *pb.ConfiguredKubernetesObjectData) {
	c.objects[id] = obj
}

// DeleteUnlocked removes an object from the cache by ID, when you already hold the lock.
// Caller MUST hold Mutex before calling this method.
func (c *ConfiguredObjectCache) DeleteUnlocked(id string) {
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

// Get retrieves an object by ID. Returns nil and false if not found.
func (c *ConfiguredObjectCache) Get(id string) (*pb.ConfiguredKubernetesObjectData, bool) {
	c.Mutex.RLock()
	defer c.Mutex.RUnlock()

	obj, ok := c.objects[id]

	return obj, ok
}

// List returns all objects in the cache, sorted by ID for consistency.
func (c *ConfiguredObjectCache) List() []*pb.ConfiguredKubernetesObjectData {
	c.Mutex.RLock()
	defer c.Mutex.RUnlock()

	return c.listUnlocked()
}

// listUnlocked returns all objects sorted by ID, when you already hold the lock.
// Caller MUST hold at least a read lock.
func (c *ConfiguredObjectCache) listUnlocked() []*pb.ConfiguredKubernetesObjectData {
	result := make([]*pb.ConfiguredKubernetesObjectData, 0, len(c.objects))
	for _, obj := range c.objects {
		result = append(result, obj)
	}

	// Sort by ID for consistent ordering
	sort.Slice(result, func(i, j int) bool {
		return result[i].GetId() < result[j].GetId()
	})

	return result
}

// Len returns the number of objects in the cache.
func (c *ConfiguredObjectCache) Len() int {
	c.Mutex.RLock()
	defer c.Mutex.RUnlock()

	return len(c.objects)
}

// IsSnapshotComplete returns whether the initial snapshot has been fully received.
// It checks if the ready channel is closed (non-blocking).
func (c *ConfiguredObjectCache) NotifyReady() bool {
	select {
	case <-c.ready:
		return true // Channel closed = snapshot complete
	default:
		return false // Channel open = still ingesting
	}
}
