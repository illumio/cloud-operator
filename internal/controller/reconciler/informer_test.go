// Copyright 2026 Illumio, Inc. All Rights Reserved.

package reconciler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/illumio/cloud-operator/internal/controller/stream/config/cache"
)

func TestNewRuntimeCache(t *testing.T) {
	runtimeCache := cache.NewRuntimeCache()

	assert.NotNil(t, runtimeCache)
	assert.Equal(t, 0, runtimeCache.Len())
	assert.Nil(t, runtimeCache.Values())
}

func TestRuntimeCache_InsertAndGet(t *testing.T) {
	runtimeCache := cache.NewRuntimeCache()

	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{
				"name":      "test-policy",
				"namespace": "default",
				"annotations": map[string]any{
					id: "policy-123",
				},
			},
		},
	}

	runtimeCache.Insert("policy-123", obj)

	assert.Equal(t, 1, runtimeCache.Len())
	retrieved := runtimeCache.Get("policy-123")
	assert.NotNil(t, retrieved)
	assert.Equal(t, "test-policy", retrieved.GetName())
}

func TestRuntimeCache_Delete(t *testing.T) {
	runtimeCache := cache.NewRuntimeCache()

	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{
				"name": "test-policy",
				"annotations": map[string]any{
					id: "policy-123",
				},
			},
		},
	}

	runtimeCache.Insert("policy-123", obj)
	assert.Equal(t, 1, runtimeCache.Len())

	runtimeCache.Delete("policy-123")
	assert.Equal(t, 0, runtimeCache.Len())
	assert.Nil(t, runtimeCache.Get("policy-123"))
}

func TestRuntimeCache_ReplaceAll(t *testing.T) {
	runtimeCache := cache.NewRuntimeCache()

	// Initially not ready
	select {
	case <-runtimeCache.IsReady():
		t.Fatal("Cache should not be ready yet")
	default:
		// Expected
	}

	// Build snapshot and replace
	snapshot := map[string]*unstructured.Unstructured{
		"id-1": {
			Object: map[string]any{
				"metadata": map[string]any{"name": "policy-1"},
			},
		},
		"id-2": {
			Object: map[string]any{
				"metadata": map[string]any{"name": "policy-2"},
			},
		},
	}
	runtimeCache.ReplaceAll(snapshot)

	// Should be ready now
	select {
	case <-runtimeCache.IsReady():
		// Expected
	default:
		t.Fatal("Cache should be ready")
	}

	assert.Equal(t, 2, runtimeCache.Len())
	assert.Equal(t, "policy-1", runtimeCache.Get("id-1").GetName())
	assert.Equal(t, "policy-2", runtimeCache.Get("id-2").GetName())
}

func TestRuntimeCache_Values(t *testing.T) {
	runtimeCache := cache.NewRuntimeCache()

	runtimeCache.Insert("id-2", &unstructured.Unstructured{
		Object: map[string]any{"metadata": map[string]any{"name": "policy-2"}},
	})
	runtimeCache.Insert("id-1", &unstructured.Unstructured{
		Object: map[string]any{"metadata": map[string]any{"name": "policy-1"}},
	})
	runtimeCache.Insert("id-3", &unstructured.Unstructured{
		Object: map[string]any{"metadata": map[string]any{"name": "policy-3"}},
	})

	values := runtimeCache.Values()
	assert.Len(t, values, 3)

	// Should be sorted by ID
	assert.Equal(t, "policy-1", values[0].GetName())
	assert.Equal(t, "policy-2", values[1].GetName())
	assert.Equal(t, "policy-3", values[2].GetName())
}
