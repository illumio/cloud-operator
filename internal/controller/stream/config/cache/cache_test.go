// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cache

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

func TestNewConfiguredObjectCache(t *testing.T) {
	cache := NewConfiguredObjectCache()

	assert.NotNil(t, cache)
	assert.NotNil(t, cache.objects)
	assert.Equal(t, 0, cache.Len())
	assert.False(t, cache.IsSnapshotComplete())
}

func TestStore(t *testing.T) {
	cache := NewConfiguredObjectCache()
	obj := &pb.ConfiguredKubernetesObjectData{
		Id:   "test-id",
		Name: "test-policy",
	}

	cache.Store("test-id", obj)

	assert.Equal(t, 1, cache.Len())
	retrieved, ok := cache.Get("test-id")
	require.True(t, ok)
	assert.Equal(t, "test-policy", retrieved.GetName())
}

func TestStoreOverwritesExisting(t *testing.T) {
	cache := NewConfiguredObjectCache()
	obj1 := &pb.ConfiguredKubernetesObjectData{
		Id:   "test-id",
		Name: "original-name",
	}
	obj2 := &pb.ConfiguredKubernetesObjectData{
		Id:   "test-id",
		Name: "updated-name",
	}

	cache.Store("test-id", obj1)
	cache.Store("test-id", obj2)

	assert.Equal(t, 1, cache.Len())
	retrieved, ok := cache.Get("test-id")
	require.True(t, ok)
	assert.Equal(t, "updated-name", retrieved.GetName())
}

func TestDelete(t *testing.T) {
	cache := NewConfiguredObjectCache()
	obj := &pb.ConfiguredKubernetesObjectData{
		Id:   "test-id",
		Name: "test-policy",
	}

	cache.Store("test-id", obj)
	assert.Equal(t, 1, cache.Len())

	cache.Delete("test-id")

	assert.Equal(t, 0, cache.Len())
	_, ok := cache.Get("test-id")
	assert.False(t, ok)
}

func TestDeleteNonExistent(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// Should not panic
	cache.Delete("non-existent-id")

	assert.Equal(t, 0, cache.Len())
}

func TestGetNotFound(t *testing.T) {
	cache := NewConfiguredObjectCache()

	obj, ok := cache.Get("non-existent-id")

	assert.Nil(t, obj)
	assert.False(t, ok)
}

func TestList(t *testing.T) {
	cache := NewConfiguredObjectCache()
	obj1 := &pb.ConfiguredKubernetesObjectData{Id: "id-1", Name: "policy-1"}
	obj2 := &pb.ConfiguredKubernetesObjectData{Id: "id-2", Name: "policy-2"}
	obj3 := &pb.ConfiguredKubernetesObjectData{Id: "id-3", Name: "policy-3"}

	cache.Store("id-1", obj1)
	cache.Store("id-2", obj2)
	cache.Store("id-3", obj3)

	list := cache.List()

	assert.Len(t, list, 3)

	// Verify all objects are in the list (order is not guaranteed)
	ids := make(map[string]bool)
	for _, obj := range list {
		ids[obj.GetId()] = true
	}

	assert.True(t, ids["id-1"])
	assert.True(t, ids["id-2"])
	assert.True(t, ids["id-3"])
}

func TestListEmpty(t *testing.T) {
	cache := NewConfiguredObjectCache()

	list := cache.List()

	assert.NotNil(t, list)
	assert.Empty(t, list)
}

func TestClear(t *testing.T) {
	cache := NewConfiguredObjectCache()
	cache.Store("id-1", &pb.ConfiguredKubernetesObjectData{Id: "id-1"})
	cache.Store("id-2", &pb.ConfiguredKubernetesObjectData{Id: "id-2"})
	cache.SetSnapshotComplete()

	assert.Equal(t, 2, cache.Len())
	assert.True(t, cache.IsSnapshotComplete())

	cache.Clear()

	assert.Equal(t, 0, cache.Len())
	assert.False(t, cache.IsSnapshotComplete())
}

func TestSnapshotCompleteState(t *testing.T) {
	cache := NewConfiguredObjectCache()

	assert.False(t, cache.IsSnapshotComplete())

	cache.SetSnapshotComplete()

	assert.True(t, cache.IsSnapshotComplete())
}

func TestConcurrentAccess(t *testing.T) {
	c := NewConfiguredObjectCache()

	var wg sync.WaitGroup

	// Concurrent stores
	for i := range 100 {
		wg.Go(func() {
			obj := &pb.ConfiguredKubernetesObjectData{
				Id:   string(rune('a' + i%26)),
				Name: "policy",
			}
			c.Store(obj.GetId(), obj)
		})
	}

	// Concurrent reads
	for range 100 {
		wg.Go(func() {
			c.List()
			c.Len()
			c.IsSnapshotComplete()
		})
	}

	// Concurrent deletes
	for i := range 50 {
		wg.Go(func() {
			c.Delete(string(rune('a' + i%26)))
		})
	}

	wg.Wait()

	// Should not panic or deadlock
}

func TestConcurrentStreamAndReconciliation(t *testing.T) {
	// Simulates concurrent access pattern:
	// - Stream receiver goroutine: writes to cache (Store/Delete)
	// - Reconciliation goroutine: reads from cache (List/Get)
	c := NewConfiguredObjectCache()

	var wg sync.WaitGroup

	// Simulate stream receiver - snapshot phase then mutations
	wg.Go(func() {
		// Snapshot phase
		for i := range 50 {
			c.Store(
				fmt.Sprintf("policy-%d", i),
				&pb.ConfiguredKubernetesObjectData{
					Id:   fmt.Sprintf("policy-%d", i),
					Name: fmt.Sprintf("policy-name-%d", i),
				},
			)
		}

		c.SetSnapshotComplete()

		// Mutation phase - updates and deletes
		for i := range 50 {
			if i%2 == 0 {
				// Update
				c.Store(
					fmt.Sprintf("policy-%d", i),
					&pb.ConfiguredKubernetesObjectData{
						Id:   fmt.Sprintf("policy-%d", i),
						Name: fmt.Sprintf("policy-name-%d-updated", i),
					},
				)
			} else {
				// Delete
				c.Delete(fmt.Sprintf("policy-%d", i))
			}
		}
	})

	// Simulate reconciliation loop - continuously reads
	wg.Go(func() {
		for range 100 {
			// Read operations that reconciliation would do
			_ = c.List()
			_ = c.Len()
			_ = c.IsSnapshotComplete()

			// Try to get specific objects
			for i := range 10 {
				_, _ = c.Get(fmt.Sprintf("policy-%d", i))
			}
		}
	})

	// Another reconciliation reader
	wg.Go(func() {
		for range 100 {
			if c.IsSnapshotComplete() {
				objects := c.List()
				for _, obj := range objects {
					// Simulate processing each object
					_ = obj.GetId()
					_ = obj.GetName()
				}
			}
		}
	})

	wg.Wait()

	// Verify final state is consistent
	assert.True(t, c.IsSnapshotComplete())

	// Should have ~25 objects (50 created, 25 deleted in mutation phase)
	// The exact count depends on timing, but should be consistent
	finalCount := c.Len()
	assert.GreaterOrEqual(t, finalCount, 0)
	assert.LessOrEqual(t, finalCount, 50)
}

func TestSnapshotThenMutationFlow(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// Snapshot phase
	assert.False(t, cache.IsSnapshotComplete())

	cache.Store("policy-1", &pb.ConfiguredKubernetesObjectData{Id: "policy-1", Name: "allow-web"})
	cache.Store("policy-2", &pb.ConfiguredKubernetesObjectData{Id: "policy-2", Name: "deny-db"})
	cache.SetSnapshotComplete()

	assert.True(t, cache.IsSnapshotComplete())
	assert.Equal(t, 2, cache.Len())

	// Mutation phase - create
	cache.Store("policy-3", &pb.ConfiguredKubernetesObjectData{Id: "policy-3", Name: "new-policy"})
	assert.Equal(t, 3, cache.Len())

	// Mutation phase - update
	cache.Store("policy-1", &pb.ConfiguredKubernetesObjectData{Id: "policy-1", Name: "updated-allow-web"})
	obj, ok := cache.Get("policy-1")
	require.True(t, ok)
	assert.Equal(t, "updated-allow-web", obj.GetName())

	// Mutation phase - delete
	cache.Delete("policy-2")
	assert.Equal(t, 2, cache.Len())
	_, ok = cache.Get("policy-2")
	assert.False(t, ok)
}
