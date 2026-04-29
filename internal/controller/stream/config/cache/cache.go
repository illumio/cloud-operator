// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cache

import (
	"sync"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// ConfiguredObjectCache stores configured Kubernetes objects received from CloudSecure.
// It tracks the desired state that CloudSecure wants in the cluster.
type ConfiguredObjectCache struct {
	mu               sync.RWMutex
	objects          map[string]*pb.ConfiguredKubernetesObjectData
	snapshotComplete bool
}

// NewConfiguredObjectCache creates a new cache instance.
func NewConfiguredObjectCache() *ConfiguredObjectCache {
	return &ConfiguredObjectCache{
		objects: make(map[string]*pb.ConfiguredKubernetesObjectData),
	}
}

// Store adds or updates an object in the cache.
func (c *ConfiguredObjectCache) Store(id string, obj *pb.ConfiguredKubernetesObjectData) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.objects[id] = obj
}

// Delete removes an object from the cache by ID.
func (c *ConfiguredObjectCache) Delete(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.objects, id)
}

// Get retrieves an object by ID. Returns nil and false if not found.
func (c *ConfiguredObjectCache) Get(id string) (*pb.ConfiguredKubernetesObjectData, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	obj, ok := c.objects[id]

	return obj, ok
}

// List returns all objects in the cache.
func (c *ConfiguredObjectCache) List() []*pb.ConfiguredKubernetesObjectData {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]*pb.ConfiguredKubernetesObjectData, 0, len(c.objects))
	for _, obj := range c.objects {
		result = append(result, obj)
	}

	return result
}

// Len returns the number of objects in the cache.
func (c *ConfiguredObjectCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.objects)
}

// Clear removes all objects from the cache and resets snapshotComplete to false.
// This should be called on stream reconnection.
func (c *ConfiguredObjectCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.objects = make(map[string]*pb.ConfiguredKubernetesObjectData)
	c.snapshotComplete = false
}

// IsSnapshotComplete returns whether the initial snapshot has been fully received.
func (c *ConfiguredObjectCache) IsSnapshotComplete() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.snapshotComplete
}

// SetSnapshotComplete marks the snapshot as complete.
// After this, only mutations should be processed, not resource data.
func (c *ConfiguredObjectCache) SetSnapshotComplete() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.snapshotComplete = true
}
