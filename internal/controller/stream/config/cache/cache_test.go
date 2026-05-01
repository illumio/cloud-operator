// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cache

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// isReady is a test helper to check if the cache's Ready channel is closed.
func isReady(c *ConfiguredObjectCache) bool {
	select {
	case <-c.IsReady():
		return true
	default:
		return false
	}
}

func TestNewConfiguredObjectCache(t *testing.T) {
	cache := NewConfiguredObjectCache()

	assert.NotNil(t, cache)
	assert.Equal(t, 0, cache.Len())
	assert.Nil(t, cache.Values())
	assert.False(t, isReady(cache))
}

func TestInsert(t *testing.T) {
	cache := NewConfiguredObjectCache()
	obj := &pb.ConfiguredKubernetesObjectData{
		Id:   "test-id",
		Name: "test-policy",
	}

	cache.Insert("test-id", obj)

	assert.Equal(t, 1, cache.Len())
	retrieved := cache.Get("test-id")
	require.NotNil(t, retrieved)
	assert.Equal(t, "test-policy", retrieved.GetName())
}

func TestInsertOverwritesExisting(t *testing.T) {
	cache := NewConfiguredObjectCache()
	obj1 := &pb.ConfiguredKubernetesObjectData{
		Id:   "test-id",
		Name: "original-name",
	}
	obj2 := &pb.ConfiguredKubernetesObjectData{
		Id:   "test-id",
		Name: "updated-name",
	}

	cache.Insert("test-id", obj1)
	cache.Insert("test-id", obj2)

	assert.Equal(t, 1, cache.Len())
	retrieved := cache.Get("test-id")
	require.NotNil(t, retrieved)
	assert.Equal(t, "updated-name", retrieved.GetName())
}

func TestDelete(t *testing.T) {
	cache := NewConfiguredObjectCache()
	obj := &pb.ConfiguredKubernetesObjectData{
		Id:   "test-id",
		Name: "test-policy",
	}

	cache.Insert("test-id", obj)
	assert.Equal(t, 1, cache.Len())

	cache.Delete("test-id")

	assert.Equal(t, 0, cache.Len())
	assert.Nil(t, cache.Get("test-id"))
}

func TestDeleteNonExistent(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// Should not panic
	cache.Delete("non-existent-id")

	assert.Equal(t, 0, cache.Len())
}

func TestGetNotFound(t *testing.T) {
	cache := NewConfiguredObjectCache()

	obj := cache.Get("non-existent-id")

	assert.Nil(t, obj)
}

func TestValuesSortedByID(t *testing.T) {
	cache := NewConfiguredObjectCache()

	cache.Insert("id-3", &pb.ConfiguredKubernetesObjectData{Id: "id-3", Name: "policy-3"})
	cache.Insert("id-1", &pb.ConfiguredKubernetesObjectData{Id: "id-1", Name: "policy-1"})
	cache.Insert("id-2", &pb.ConfiguredKubernetesObjectData{Id: "id-2", Name: "policy-2"})

	list := cache.Values()

	assert.Len(t, list, 3)
	// Values should be sorted by ID
	assert.Equal(t, "id-1", list[0].GetId())
	assert.Equal(t, "id-2", list[1].GetId())
	assert.Equal(t, "id-3", list[2].GetId())
}

func TestValuesEmpty(t *testing.T) {
	cache := NewConfiguredObjectCache()

	list := cache.Values()

	assert.Nil(t, list) // Returns nil for empty cache
}

func TestReplaceAll(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// Initially not ready
	assert.False(t, isReady(cache))

	// Build snapshot and replace
	snapshot := map[string]*pb.ConfiguredKubernetesObjectData{
		"id-1": {Id: "id-1", Name: "policy-1"},
		"id-2": {Id: "id-2", Name: "policy-2"},
	}
	cache.ReplaceAll(snapshot)

	assert.True(t, isReady(cache))
	assert.Equal(t, 2, cache.Len())
	assert.Equal(t, "policy-1", cache.Get("id-1").GetName())
	assert.Equal(t, "policy-2", cache.Get("id-2").GetName())
}

func TestReplaceAllWithEmptyMap(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// ReplaceAll with empty map should still mark ready
	cache.ReplaceAll(make(map[string]*pb.ConfiguredKubernetesObjectData))

	assert.True(t, isReady(cache))
	assert.Equal(t, 0, cache.Len())
}

func TestReplaceAllReplacesExisting(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// First snapshot
	snapshot1 := map[string]*pb.ConfiguredKubernetesObjectData{
		"id-1": {Id: "id-1", Name: "v1"},
	}
	cache.ReplaceAll(snapshot1)

	assert.Equal(t, 1, cache.Len())
	assert.Equal(t, "v1", cache.Get("id-1").GetName())

	// Second snapshot (simulates reconnect) - completely replaces
	snapshot2 := map[string]*pb.ConfiguredKubernetesObjectData{
		"id-1": {Id: "id-1", Name: "v2"},
		"id-2": {Id: "id-2", Name: "new"},
	}
	cache.ReplaceAll(snapshot2)

	assert.Equal(t, 2, cache.Len())
	assert.Equal(t, "v2", cache.Get("id-1").GetName())
	assert.Equal(t, "new", cache.Get("id-2").GetName())
}

func TestReadyChannel(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// Ready channel should be open (not complete)
	select {
	case <-cache.IsReady():
		t.Fatal("Ready channel should not be closed yet")
	default:
		// Expected
	}

	// Complete the snapshot via ReplaceAll
	cache.ReplaceAll(make(map[string]*pb.ConfiguredKubernetesObjectData))

	// Ready channel should be closed now
	select {
	case <-cache.IsReady():
		// Expected
	default:
		t.Fatal("Ready channel should be closed")
	}
}

func TestReplaceAllIdempotentReady(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// First call marks ready
	cache.ReplaceAll(map[string]*pb.ConfiguredKubernetesObjectData{
		"id-1": {Id: "id-1"},
	})
	assert.True(t, isReady(cache))

	// Second call should not panic (idempotent)
	cache.ReplaceAll(map[string]*pb.ConfiguredKubernetesObjectData{
		"id-2": {Id: "id-2"},
	})
	assert.True(t, isReady(cache))
	assert.Equal(t, 1, cache.Len()) // Only id-2
}

func TestReadyChannelBlocksUntilSnapshotComplete(t *testing.T) {
	cache := NewConfiguredObjectCache()

	done := make(chan struct{})

	go func() {
		<-cache.IsReady() // Should block until ReplaceAll
		close(done)
	}()

	// Goroutine should be blocked
	select {
	case <-done:
		t.Fatal("Goroutine should be blocked waiting for Ready")
	default:
		// Expected
	}

	// Complete snapshot
	cache.ReplaceAll(make(map[string]*pb.ConfiguredKubernetesObjectData))

	// Now goroutine should unblock
	<-done
}

func TestSnapshotThenMutationFlow(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// Snapshot phase - build local map
	assert.False(t, isReady(cache))

	snapshot := map[string]*pb.ConfiguredKubernetesObjectData{
		"policy-1": {Id: "policy-1", Name: "allow-web"},
		"policy-2": {Id: "policy-2", Name: "deny-db"},
	}
	cache.ReplaceAll(snapshot)

	assert.True(t, isReady(cache))
	assert.Equal(t, 2, cache.Len())

	// Mutation phase - create
	cache.Insert("policy-3", &pb.ConfiguredKubernetesObjectData{Id: "policy-3", Name: "new-policy"})
	assert.Equal(t, 3, cache.Len())

	// Mutation phase - update
	cache.Insert("policy-1", &pb.ConfiguredKubernetesObjectData{Id: "policy-1", Name: "updated-allow-web"})
	obj := cache.Get("policy-1")
	require.NotNil(t, obj)
	assert.Equal(t, "updated-allow-web", obj.GetName())

	// Mutation phase - delete
	cache.Delete("policy-2")
	assert.Equal(t, 2, cache.Len())
	assert.Nil(t, cache.Get("policy-2"))
}

func TestConcurrentMutationsAfterSnapshot(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// Complete initial snapshot
	cache.ReplaceAll(make(map[string]*pb.ConfiguredKubernetesObjectData))

	var wg sync.WaitGroup

	// Concurrent inserts
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cache.Insert(
				string(rune('a'+i%26)),
				&pb.ConfiguredKubernetesObjectData{Id: string(rune('a' + i%26)), Name: "policy"},
			)
		}(i)
	}

	// Concurrent reads
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cache.Values()
			_ = cache.Len()
			_ = isReady(cache)
		}()
	}

	// Concurrent deletes
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cache.Delete(string(rune('a' + i%26)))
		}(i)
	}

	wg.Wait()

	// Should not panic or deadlock
	assert.True(t, isReady(cache))
}

func TestMultipleSnapshots(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// First snapshot
	cache.ReplaceAll(map[string]*pb.ConfiguredKubernetesObjectData{
		"id-1": {Id: "id-1", Name: "v1"},
	})

	assert.Equal(t, 1, cache.Len())
	assert.Equal(t, "v1", cache.Get("id-1").GetName())

	// Second snapshot (simulates reconnect) - completely replaces
	cache.ReplaceAll(map[string]*pb.ConfiguredKubernetesObjectData{
		"id-1": {Id: "id-1", Name: "v2"},
		"id-2": {Id: "id-2", Name: "new"},
	})

	assert.Equal(t, 2, cache.Len())
	assert.Equal(t, "v2", cache.Get("id-1").GetName())
	assert.Equal(t, "new", cache.Get("id-2").GetName())

	// Ready should still be true
	assert.True(t, isReady(cache))
}

func TestConcurrentReplaceAllAndReads(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// Initial snapshot
	cache.ReplaceAll(map[string]*pb.ConfiguredKubernetesObjectData{
		"id-1": {Id: "id-1", Name: "initial"},
	})

	var wg sync.WaitGroup

	// Concurrent ReplaceAll calls (simulating rapid reconnects)
	for i := range 10 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cache.ReplaceAll(map[string]*pb.ConfiguredKubernetesObjectData{
				"id-1": {Id: "id-1", Name: "version-" + string(rune('0'+i))},
			})
		}(i)
	}

	// Concurrent reads
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cache.Get("id-1")
			_ = cache.Values()
			_ = cache.Len()
		}()
	}

	wg.Wait()

	// Should not panic or deadlock
	assert.True(t, isReady(cache))
	assert.Equal(t, 1, cache.Len())
}

func TestAtomicSwap_ReadersNeverSeePartialData(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// Create two distinct snapshots - each has objects with matching "version" in name
	snapshotA := map[string]*pb.ConfiguredKubernetesObjectData{
		"obj-1": {Id: "obj-1", Name: "A-1"},
		"obj-2": {Id: "obj-2", Name: "A-2"},
		"obj-3": {Id: "obj-3", Name: "A-3"},
	}
	snapshotB := map[string]*pb.ConfiguredKubernetesObjectData{
		"obj-1": {Id: "obj-1", Name: "B-1"},
		"obj-2": {Id: "obj-2", Name: "B-2"},
		"obj-3": {Id: "obj-3", Name: "B-3"},
	}

	// Start with snapshot A
	cache.ReplaceAll(snapshotA)

	var wg sync.WaitGroup
	inconsistentRead := false

	// Writer: swap between A and B repeatedly
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			if i%2 == 0 {
				cache.ReplaceAll(snapshotB)
			} else {
				cache.ReplaceAll(snapshotA)
			}
		}
	}()

	// Readers: verify all objects have same version prefix
	for r := 0; r < 10; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				values := cache.Values()
				if len(values) == 0 {
					continue
				}

				// All objects should have same prefix (A- or B-)
				firstPrefix := values[0].GetName()[:2]
				for _, v := range values {
					if v.GetName()[:2] != firstPrefix {
						inconsistentRead = true
						return
					}
				}
			}
		}()
	}

	wg.Wait()

	assert.False(t, inconsistentRead, "Reader saw mixed data from different snapshots")
}

func TestAtomicSwap_ReadersNeverBlockForever(t *testing.T) {
	cache := NewConfiguredObjectCache()
	cache.ReplaceAll(map[string]*pb.ConfiguredKubernetesObjectData{
		"id-1": {Id: "id-1", Name: "initial"},
	})

	// Channel to signal reader completion
	done := make(chan struct{})

	// Writer: continuously swap data
	go func() {
		for i := 0; i < 10000; i++ {
			cache.ReplaceAll(map[string]*pb.ConfiguredKubernetesObjectData{
				"id-1": {Id: "id-1", Name: "version"},
			})
		}
	}()

	// Reader: read continuously while writer is swapping
	go func() {
		for i := 0; i < 10000; i++ {
			_ = cache.Values()
			_ = cache.Get("id-1")
			_ = cache.Len()
		}
		close(done)
	}()

	// Wait with timeout - if reader blocks forever, test fails
	select {
	case <-done:
		// Success - reader completed
	case <-time.After(5 * time.Second):
		t.Fatal("Reader blocked forever - possible deadlock")
	}
}
