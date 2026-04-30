// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cache

import (
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
	assert.False(t, cache.NotifyReady())
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

func TestBeginEndSnapshot(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// Initially not complete
	assert.False(t, cache.NotifyReady())

	// Begin snapshot - locks cache
	cache.BeginSnapshot()

	// Insert during snapshot (unlocked method since we hold lock)
	cache.InsertLocked("id-1", &pb.ConfiguredKubernetesObjectData{Id: "id-1"})
	cache.InsertLocked("id-2", &pb.ConfiguredKubernetesObjectData{Id: "id-2"})

	// End snapshot - unlocks cache and signals ready
	cache.EndSnapshot()

	assert.True(t, cache.NotifyReady())
	assert.Equal(t, 2, cache.Len())
}

func TestBeginSnapshotClearsCache(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// Add some data and complete first snapshot
	cache.BeginSnapshot()
	cache.InsertLocked("id-1", &pb.ConfiguredKubernetesObjectData{Id: "id-1"})
	cache.EndSnapshot()

	assert.Equal(t, 1, cache.Len())

	// Begin new snapshot (simulates reconnect) - should clear
	cache.BeginSnapshot()
	assert.Equal(t, 0, cache.LenUnlocked()) // Use LenUnlocked since we hold the lock
	cache.EndSnapshot()
}

func TestReadyChannel(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// Ready channel should be open (not complete)
	select {
	case <-cache.Ready():
		t.Fatal("Ready channel should not be closed yet")
	default:
		// Expected
	}

	// Complete the snapshot
	cache.BeginSnapshot()
	cache.EndSnapshot()

	// Ready channel should be closed now
	select {
	case <-cache.Ready():
		// Expected
	default:
		t.Fatal("Ready channel should be closed")
	}
}

func TestReadyChannelClosedOnlyOnce(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// First snapshot
	cache.BeginSnapshot()
	cache.EndSnapshot()

	// Channel should be closed
	assert.True(t, cache.NotifyReady())

	// Second snapshot (reconnect) - should not panic
	cache.BeginSnapshot()
	cache.EndSnapshot()

	// Still complete
	assert.True(t, cache.NotifyReady())
}

func TestReadyChannelBlocksUntilSnapshotComplete(t *testing.T) {
	cache := NewConfiguredObjectCache()

	done := make(chan struct{})

	go func() {
		<-cache.Ready() // Should block until EndSnapshot
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
	cache.BeginSnapshot()
	cache.EndSnapshot()

	// Now goroutine should unblock
	<-done
}

func TestConcurrentReadersBlockedDuringSnapshot(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// Begin snapshot - acquires write lock
	cache.BeginSnapshot()

	// Try to read in another goroutine - should block
	readStarted := make(chan struct{})
	readDone := make(chan struct{})

	go func() {
		close(readStarted)
		_ = cache.Values() // This should block until EndSnapshot
		close(readDone)
	}()

	<-readStarted

	// Reader should be blocked
	select {
	case <-readDone:
		t.Fatal("Reader should be blocked during snapshot")
	default:
		// Expected - reader is blocked
	}

	// End snapshot - releases lock
	cache.EndSnapshot()

	// Now reader should complete
	<-readDone
}

func TestSnapshotThenMutationFlow(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// Snapshot phase
	assert.False(t, cache.NotifyReady())

	cache.BeginSnapshot()
	cache.InsertLocked("policy-1", &pb.ConfiguredKubernetesObjectData{Id: "policy-1", Name: "allow-web"})
	cache.InsertLocked("policy-2", &pb.ConfiguredKubernetesObjectData{Id: "policy-2", Name: "deny-db"})
	cache.EndSnapshot()

	assert.True(t, cache.NotifyReady())
	assert.Equal(t, 2, cache.Len())

	// Mutation phase - create (uses locked method)
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
	cache.BeginSnapshot()
	cache.EndSnapshot()

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
			_ = cache.NotifyReady()
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
	assert.True(t, cache.NotifyReady())
}

func TestMultipleSnapshots(t *testing.T) {
	cache := NewConfiguredObjectCache()

	// First snapshot
	cache.BeginSnapshot()
	cache.InsertLocked("id-1", &pb.ConfiguredKubernetesObjectData{Id: "id-1", Name: "v1"})
	cache.EndSnapshot()

	assert.Equal(t, 1, cache.Len())
	obj := cache.Get("id-1")
	assert.Equal(t, "v1", obj.GetName())

	// Second snapshot (simulates reconnect) - should clear and reset
	cache.BeginSnapshot()
	assert.Equal(t, 0, cache.LenUnlocked()) // Cleared - use LenUnlocked since we hold the lock

	cache.InsertLocked("id-1", &pb.ConfiguredKubernetesObjectData{Id: "id-1", Name: "v2"})
	cache.InsertLocked("id-2", &pb.ConfiguredKubernetesObjectData{Id: "id-2", Name: "new"})
	cache.EndSnapshot()

	assert.Equal(t, 2, cache.Len())
	obj = cache.Get("id-1")
	assert.Equal(t, "v2", obj.GetName())

	// Ready should still be true (channel was closed on first snapshot)
	assert.True(t, cache.NotifyReady())
}
