// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cache

import (
	"context"
	"strconv"
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
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

	obj := &pb.ConfiguredKubernetesObjectData{
		Id:   "test-id",
		Name: "test-policy",
	}

	go func() {
		err := cache.Insert(ctx, "test-id", obj)

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	assert.Equal(t, 1, cache.Len())
	retrieved := cache.Get("test-id")
	require.NotNil(t, retrieved)
	assert.Equal(t, "test-policy", retrieved.GetName())
}

func TestInsertOverwritesExisting(t *testing.T) {
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

	obj1 := &pb.ConfiguredKubernetesObjectData{
		Id:   "test-id",
		Name: "original-name",
	}
	obj2 := &pb.ConfiguredKubernetesObjectData{
		Id:   "test-id",
		Name: "updated-name",
	}

	go func() {
		err := cache.Insert(ctx, "test-id", obj1)

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	go func() {
		err := cache.Insert(ctx, "test-id", obj2)

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	assert.Equal(t, 1, cache.Len())
	retrieved := cache.Get("test-id")
	require.NotNil(t, retrieved)
	assert.Equal(t, "updated-name", retrieved.GetName())
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

	obj := &pb.ConfiguredKubernetesObjectData{
		Id:   "test-id",
		Name: "test-policy",
	}

	go func() {
		err := cache.Insert(ctx, "test-id", obj)

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()
	assert.Equal(t, 1, cache.Len())

	go func() {
		err := cache.Delete(ctx, "test-id")

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	assert.Equal(t, 0, cache.Len())
	assert.Nil(t, cache.Get("test-id"))
}

func TestDeleteNonExistentSkipsNotification(t *testing.T) {
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

	// Pre-spawn collector to count all notifications.
	var collected []string

	collectorDone := make(chan struct{})

	go func() {
		for id := range cache.ResourceChanged() {
			collected = append(collected, id)
		}

		close(collectorDone)
	}()

	// Insert an object, should notify.
	err := cache.Insert(ctx, "test-id", &pb.ConfiguredKubernetesObjectData{
		Id:   "test-id",
		Name: "test-policy",
	})

	require.NoError(t, err)

	// Delete it, should notify.
	err = cache.Delete(ctx, "test-id")

	require.NoError(t, err)

	// Delete it again (non-existent), should NOT notify.
	err = cache.Delete(ctx, "test-id")

	require.NoError(t, err)

	// Delete something that never existed, should NOT notify.
	err = cache.Delete(ctx, "never-existed")

	require.NoError(t, err)

	cache.Close()
	<-collectorDone

	assert.Equal(t, 0, cache.Len())
	assert.Equal(t, []string{"test-id", "test-id"}, collected, "Should notify exactly twice: insert and first delete, not the non-existent deletes")
}

func TestInsertIdenticalSkipsNotification(t *testing.T) {
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

	// Pre-spawn collector to count all notifications.
	var collected []string

	collectorDone := make(chan struct{})

	go func() {
		for id := range cache.ResourceChanged() {
			collected = append(collected, id)
		}

		close(collectorDone)
	}()

	// First insert: new object, should notify.
	err := cache.Insert(ctx, "test-id", &pb.ConfiguredKubernetesObjectData{
		Id:   "test-id",
		Name: "test-policy",
	})

	require.NoError(t, err)

	// Second insert: identical object, should NOT notify.
	err = cache.Insert(ctx, "test-id", &pb.ConfiguredKubernetesObjectData{
		Id:   "test-id",
		Name: "test-policy",
	})

	require.NoError(t, err)

	// Third insert: changed object, should notify.
	err = cache.Insert(ctx, "test-id", &pb.ConfiguredKubernetesObjectData{
		Id:   "test-id",
		Name: "updated-policy",
	})

	require.NoError(t, err)

	cache.Close()
	<-collectorDone

	assert.Equal(t, 1, cache.Len())
	assert.Equal(t, []string{"test-id", "test-id"}, collected, "Should notify exactly twice: first insert and update, not the identical insert")
}

func TestGetNotFound(t *testing.T) {
	cache := NewConfiguredObjectCache()

	obj := cache.Get("non-existent-id")

	assert.Nil(t, obj)
}

func TestValuesSortedByID(t *testing.T) {
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

	go func() {
		err := cache.Insert(ctx, "id-3", &pb.ConfiguredKubernetesObjectData{Id: "id-3", Name: "policy-3"})

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	go func() {
		err := cache.Insert(ctx, "id-1", &pb.ConfiguredKubernetesObjectData{Id: "id-1", Name: "policy-1"})

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	go func() {
		err := cache.Insert(ctx, "id-2", &pb.ConfiguredKubernetesObjectData{Id: "id-2", Name: "policy-2"})

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	list := cache.Values()

	assert.Len(t, list, 3)
	assert.Equal(t, "id-1", list[0].GetId())
	assert.Equal(t, "id-2", list[1].GetId())
	assert.Equal(t, "id-3", list[2].GetId())
}

func TestValuesEmpty(t *testing.T) {
	cache := NewConfiguredObjectCache()

	list := cache.Values()

	assert.Nil(t, list)
}

func TestReplaceAll(t *testing.T) {
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

	assert.False(t, isReady(cache))

	snapshot := map[string]*pb.ConfiguredKubernetesObjectData{
		"id-1": {Id: "id-1", Name: "policy-1"},
		"id-2": {Id: "id-2", Name: "policy-2"},
	}

	go func() {
		err := cache.ReplaceAll(ctx, snapshot)

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	assert.True(t, isReady(cache))
	assert.Equal(t, 2, cache.Len())
	assert.Equal(t, "policy-1", cache.Get("id-1").GetName())
	assert.Equal(t, "policy-2", cache.Get("id-2").GetName())
}

func TestReplaceAllWithEmptyMap(t *testing.T) {
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

	go func() {
		err := cache.ReplaceAll(ctx, make(map[string]*pb.ConfiguredKubernetesObjectData))

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	assert.True(t, isReady(cache))
	assert.Equal(t, 0, cache.Len())
}

func TestReplaceAllReplacesExisting(t *testing.T) {
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

	go func() {
		err := cache.ReplaceAll(ctx, map[string]*pb.ConfiguredKubernetesObjectData{
			"id-1": {Id: "id-1", Name: "v1"},
		})

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	assert.Equal(t, 1, cache.Len())
	assert.Equal(t, "v1", cache.Get("id-1").GetName())

	go func() {
		err := cache.ReplaceAll(ctx, map[string]*pb.ConfiguredKubernetesObjectData{
			"id-1": {Id: "id-1", Name: "v2"},
			"id-2": {Id: "id-2", Name: "new"},
		})

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	assert.Equal(t, 2, cache.Len())
	assert.Equal(t, "v2", cache.Get("id-1").GetName())
	assert.Equal(t, "new", cache.Get("id-2").GetName())
}

func TestReadyChannel(t *testing.T) {
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

	select {
	case <-cache.IsReady():
		t.Fatal("Ready channel should not be closed yet")
	default:
	}

	go func() {
		err := cache.ReplaceAll(ctx, make(map[string]*pb.ConfiguredKubernetesObjectData))

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	select {
	case <-cache.IsReady():
	default:
		t.Fatal("Ready channel should be closed")
	}
}

func TestReplaceAllIdempotentReady(t *testing.T) {
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

	go func() {
		err := cache.ReplaceAll(ctx, map[string]*pb.ConfiguredKubernetesObjectData{
			"id-1": {Id: "id-1"},
		})

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	assert.True(t, isReady(cache))

	go func() {
		err := cache.ReplaceAll(ctx, map[string]*pb.ConfiguredKubernetesObjectData{
			"id-2": {Id: "id-2"},
		})

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	assert.True(t, isReady(cache))
	assert.Equal(t, 1, cache.Len())
}

func TestReadyChannelBlocksUntilSnapshotComplete(t *testing.T) {
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

	done := make(chan struct{})

	go func() {
		<-cache.IsReady()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("Goroutine should be blocked waiting for Ready")
	default:
	}

	go func() {
		err := cache.ReplaceAll(ctx, make(map[string]*pb.ConfiguredKubernetesObjectData))

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	<-done
}

func TestSnapshotThenMutationFlow(t *testing.T) {
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

	assert.False(t, isReady(cache))

	snapshot := map[string]*pb.ConfiguredKubernetesObjectData{
		"policy-1": {Id: "policy-1", Name: "allow-web"},
		"policy-2": {Id: "policy-2", Name: "deny-db"},
	}

	go func() {
		err := cache.ReplaceAll(ctx, snapshot)

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	assert.True(t, isReady(cache))
	assert.Equal(t, 2, cache.Len())

	go func() {
		err := cache.Insert(ctx, "policy-3", &pb.ConfiguredKubernetesObjectData{Id: "policy-3", Name: "new-policy"})

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	assert.Equal(t, 3, cache.Len())

	go func() {
		err := cache.Insert(ctx, "policy-1", &pb.ConfiguredKubernetesObjectData{Id: "policy-1", Name: "updated-allow-web"})

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	obj := cache.Get("policy-1")
	require.NotNil(t, obj)
	assert.Equal(t, "updated-allow-web", obj.GetName())

	go func() {
		err := cache.Delete(ctx, "policy-2")

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	assert.Equal(t, 2, cache.Len())
	assert.Nil(t, cache.Get("policy-2"))
}

func TestConcurrentMutationsAfterSnapshot(t *testing.T) {
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

	go func() {
		err := cache.ReplaceAll(ctx, make(map[string]*pb.ConfiguredKubernetesObjectData))

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	// Start a reader that consumes all channel sends from concurrent operations
	readerDone := make(chan struct{})

	go func() {
		for range cache.ResourceChanged() {
		}

		close(readerDone)
	}()

	var wg sync.WaitGroup

	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			err := cache.Insert(
				ctx,
				string(rune('a'+i%26)),
				&pb.ConfiguredKubernetesObjectData{Id: string(rune('a' + i%26)), Name: "policy"},
			)

			assert.NoError(t, err)
		}(i)
	}

	for range 100 {
		wg.Go(func() {
			_ = cache.Values()
			_ = cache.Len()
			_ = isReady(cache)
		})
	}

	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			err := cache.Delete(ctx, string(rune('a'+i%26)))

			assert.NoError(t, err)
		}(i)
	}

	wg.Wait()
	cache.Close() // Stop the reader goroutine
	<-readerDone

	assert.True(t, isReady(cache))
}

func TestMultipleSnapshots(t *testing.T) {
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

	go func() {
		err := cache.ReplaceAll(ctx, map[string]*pb.ConfiguredKubernetesObjectData{
			"id-1": {Id: "id-1", Name: "v1"},
		})

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	assert.Equal(t, 1, cache.Len())
	assert.Equal(t, "v1", cache.Get("id-1").GetName())

	go func() {
		err := cache.ReplaceAll(ctx, map[string]*pb.ConfiguredKubernetesObjectData{
			"id-1": {Id: "id-1", Name: "v2"},
			"id-2": {Id: "id-2", Name: "new"},
		})

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	assert.Equal(t, 2, cache.Len())
	assert.Equal(t, "v2", cache.Get("id-1").GetName())
	assert.Equal(t, "new", cache.Get("id-2").GetName())

	assert.True(t, isReady(cache))
}

func TestConcurrentReplaceAllAndReads(t *testing.T) {
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

	go func() {
		err := cache.ReplaceAll(ctx, map[string]*pb.ConfiguredKubernetesObjectData{
			"id-1": {Id: "id-1", Name: "initial"},
		})

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	// Start a reader for concurrent ReplaceAll sends
	readerDone := make(chan struct{})

	go func() {
		for range cache.ResourceChanged() {
		}

		close(readerDone)
	}()

	var wg sync.WaitGroup

	for i := range 10 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			err := cache.ReplaceAll(ctx, map[string]*pb.ConfiguredKubernetesObjectData{
				"id-1": {Id: "id-1", Name: "version-" + strconv.Itoa(i)},
			})

			assert.NoError(t, err)
		}(i)
	}

	for range 100 {
		wg.Go(func() {
			_ = cache.Get("id-1")
			_ = cache.Values()
			_ = cache.Len()
		})
	}

	wg.Wait()
	cache.Close()
	<-readerDone

	assert.True(t, isReady(cache))
	assert.Equal(t, 1, cache.Len())
}

func TestAtomicSwap_ReadersNeverSeePartialData(t *testing.T) {
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

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

	go func() {
		err := cache.ReplaceAll(ctx, snapshotA)

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	// Reader for concurrent ReplaceAll sends
	readerDone := make(chan struct{})

	go func() {
		for range cache.ResourceChanged() {
		}

		close(readerDone)
	}()

	var wg sync.WaitGroup

	inconsistentRead := false

	wg.Go(func() {
		for i := range 1000 {
			if i%2 == 0 {
				err := cache.ReplaceAll(ctx, snapshotB)

				assert.NoError(t, err)
			} else {
				err := cache.ReplaceAll(ctx, snapshotA)

				assert.NoError(t, err)
			}
		}
	})

	for range 10 {
		wg.Go(func() {
			for range 500 {
				values := cache.Values()
				if len(values) == 0 {
					continue
				}

				firstPrefix := values[0].GetName()[:2]
				for _, v := range values {
					if v.GetName()[:2] != firstPrefix {
						inconsistentRead = true

						return
					}
				}
			}
		})
	}

	wg.Wait()
	cache.Close()
	<-readerDone

	assert.False(t, inconsistentRead, "Reader saw mixed data from different snapshots")
}

func TestAtomicSwap_ReadersNeverBlockForever(t *testing.T) {
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

	go func() {
		err := cache.ReplaceAll(ctx, map[string]*pb.ConfiguredKubernetesObjectData{
			"id-1": {Id: "id-1", Name: "initial"},
		})

		assert.NoError(t, err)
	}()

	<-cache.ResourceChanged()

	done := make(chan struct{})

	// Reader for concurrent ReplaceAll sends
	readerDone := make(chan struct{})

	go func() {
		for range cache.ResourceChanged() {
		}

		close(readerDone)
	}()

	go func() {
		for range 10000 {
			err := cache.ReplaceAll(ctx, map[string]*pb.ConfiguredKubernetesObjectData{
				"id-1": {Id: "id-1", Name: "version"},
			})

			assert.NoError(t, err)
		}

		cache.Close()
		<-readerDone
		close(done)
	}()

	go func() {
		for range 10000 {
			_ = cache.Values()
			_ = cache.Get("id-1")
			_ = cache.Len()
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Reader blocked forever - possible deadlock")
	}
}

func TestResourceChangedChannel(t *testing.T) {
	ctx := context.Background()
	cache := NewConfiguredObjectCache()

	go func() {
		err := cache.ReplaceAll(ctx, map[string]*pb.ConfiguredKubernetesObjectData{
			"id-1": {Id: "id-1"},
		})

		assert.NoError(t, err)
	}()

	id := <-cache.ResourceChanged()
	assert.Equal(t, SnapshotReplaced, id)

	go func() {
		err := cache.Insert(ctx, "id-2", &pb.ConfiguredKubernetesObjectData{Id: "id-2"})

		assert.NoError(t, err)
	}()

	id = <-cache.ResourceChanged()
	assert.Equal(t, "id-2", id)

	go func() {
		err := cache.Delete(ctx, "id-1")

		assert.NoError(t, err)
	}()

	id = <-cache.ResourceChanged()
	assert.Equal(t, "id-1", id)
}
