// Copyright 2026 Illumio, Inc. All Rights Reserved.

package reconciler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream/config/cache"
	"github.com/illumio/cloud-operator/internal/controller/stream/resources"
)

// mockClient implements k8sclient.Client for testing.
type mockClient struct {
	clientset     kubernetes.Interface
	dynamicClient *dynamicfake.FakeDynamicClient
	applyCalls    int
	deleteCalls   int
	applyErr      error
	deleteErr     error
}

func (m *mockClient) GetClientset() kubernetes.Interface {
	return m.clientset
}

func (m *mockClient) GetDynamicClient() dynamic.Interface {
	return m.dynamicClient
}

func (m *mockClient) GetDiscoveryClient() discovery.DiscoveryInterface {
	return nil
}

func (m *mockClient) GetSecret(_ context.Context, _, _ string) (*corev1.Secret, error) {
	return nil, nil //nolint:nilnil // mock implementation
}

func (m *mockClient) CreateSecret(_ context.Context, _ string, _ *corev1.Secret) (*corev1.Secret, error) {
	return nil, nil //nolint:nilnil // mock implementation
}

func (m *mockClient) UpdateSecret(_ context.Context, _ string, _ *corev1.Secret) (*corev1.Secret, error) {
	return nil, nil //nolint:nilnil // mock implementation
}

func (m *mockClient) GetResource(_ context.Context, _ schema.GroupVersionResource, _, _ string) (*unstructured.Unstructured, error) {
	return nil, nil //nolint:nilnil // mock implementation
}

func (m *mockClient) ListResources(_ context.Context, _ schema.GroupVersionResource, _ string) (*unstructured.UnstructuredList, error) {
	return nil, nil //nolint:nilnil // mock implementation
}

func (m *mockClient) WatchResources(_ context.Context, _ schema.GroupVersionResource, _ string, _ string) (watch.Interface, error) {
	return nil, nil //nolint:nilnil // mock implementation
}

func (m *mockClient) ApplyResource(_ context.Context, _ schema.GroupVersionResource, _ string, obj *unstructured.Unstructured, _ string) (*unstructured.Unstructured, error) {
	m.applyCalls++

	if m.applyErr != nil {
		return nil, m.applyErr
	}

	return obj, nil
}

func (m *mockClient) DeleteResource(_ context.Context, _ schema.GroupVersionResource, _, _ string) error {
	m.deleteCalls++

	return m.deleteErr
}

func newMockClient() *mockClient {
	scheme := runtime.NewScheme()

	return &mockClient{
		clientset:     k8sfake.NewClientset(),
		dynamicClient: dynamicfake.NewSimpleDynamicClient(scheme),
	}
}

func TestNewReconciler(t *testing.T) {
	logger := zap.NewNop()
	client := newMockClient()
	configCache := cache.NewConfiguredObjectCache()
	runtimeCache := cache.NewConfiguredObjectCache()

	r := NewReconciler(logger, client, configCache, runtimeCache)

	assert.NotNil(t, r)
	assert.Equal(t, logger, r.logger)
	assert.Equal(t, client, r.client)
	assert.Equal(t, configCache, r.configCache)
	assert.Equal(t, runtimeCache, r.runtimeCache)
}

func TestReconcile_EmptyCaches(t *testing.T) {
	logger := zap.NewNop()
	client := newMockClient()
	configCache := cache.NewConfiguredObjectCache()
	runtimeCache := cache.NewConfiguredObjectCache()

	// Mark caches as ready with empty data
	go configCache.ReplaceAll(make(map[string]*pb.ConfiguredKubernetesObjectData))

	<-configCache.ResourceChanged()

	go runtimeCache.ReplaceAll(make(map[string]*pb.ConfiguredKubernetesObjectData))

	<-runtimeCache.ResourceChanged()

	r := NewReconciler(logger, client, configCache, runtimeCache)
	// Set resourceInfo for test (normally done in Start())
	r.resourceInfo = map[string]resources.ResourceInfo{
		"ciliumnetworkpolicies": {Group: "cilium.io", Version: "v2"},
	}

	err := r.reconcileAll(context.Background())
	assert.NoError(t, err)
}

// populateCache fills a cache and drains its notifications synchronously.
// Get/Values/Len still work after Close — only ResourceChanged becomes unusable.
func populateCache(c *cache.ConfiguredObjectCache, objects map[string]*pb.ConfiguredKubernetesObjectData) {
	done := make(chan struct{})

	go func() {
		for range c.ResourceChanged() {
		}

		close(done)
	}()

	if objects != nil {
		c.ReplaceAll(objects)
	}

	c.Close()
	<-done
}

// newTestReconciler creates a reconciler with pre-populated caches for testing.
func newTestReconciler(t *testing.T, configObjects, runtimeObjects map[string]*pb.ConfiguredKubernetesObjectData) (*Reconciler, *mockClient) {
	t.Helper()

	client := newMockClient()
	configCache := cache.NewConfiguredObjectCache()
	runtimeCache := cache.NewConfiguredObjectCache()

	populateCache(configCache, configObjects)
	populateCache(runtimeCache, runtimeObjects)

	r := NewReconciler(zap.NewNop(), client, configCache, runtimeCache)
	r.resourceInfo = map[string]resources.ResourceInfo{
		"ciliumnetworkpolicies":            {Group: "cilium.io", Version: "v2"},
		"ciliumclusterwidenetworkpolicies": {Group: "cilium.io", Version: "v2"},
		"ciliumcidrgroups":                 {Group: "cilium.io", Version: "v2alpha1"},
	}

	return r, client
}

func TestReconcileObject_SkipsApplyWhenMatching(t *testing.T) {
	obj := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "allow-web",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}

	r, client := newTestReconciler(t,
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": obj},
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": obj},
	)

	err := r.reconcileObject(context.Background(), "policy-1")
	assert.NoError(t, err)
	assert.Equal(t, 0, client.applyCalls, "Should skip apply when config and runtime match")
}

func TestReconcileObject_AppliesWhenDifferent(t *testing.T) {
	configObj := &pb.ConfiguredKubernetesObjectData{
		Id:          "policy-1",
		Name:        "allow-web",
		Annotations: map[string]string{"note": "updated"},
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}
	runtimeObj := &pb.ConfiguredKubernetesObjectData{
		Id:          "policy-1",
		Name:        "allow-web",
		Annotations: map[string]string{"note": "old"},
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}

	r, client := newTestReconciler(t,
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": configObj},
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": runtimeObj},
	)

	err := r.reconcileObject(context.Background(), "policy-1")
	assert.NoError(t, err)
	assert.Equal(t, 1, client.applyCalls, "Should apply when config and runtime differ")
}

func TestReconcileObject_AppliesWhenAnnotationDeleted(t *testing.T) {
	configObj := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "allow-web",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}
	runtimeObj := &pb.ConfiguredKubernetesObjectData{
		Id:          "policy-1",
		Name:        "allow-web",
		Annotations: map[string]string{"note": "should-be-removed"},
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}

	r, client := newTestReconciler(t,
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": configObj},
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": runtimeObj},
	)

	err := r.reconcileObject(context.Background(), "policy-1")
	assert.NoError(t, err)
	assert.Equal(t, 1, client.applyCalls, "Should apply when annotation was deleted from config")
}

func TestReconcileObject_DeletesOrphanedRuntimeObject(t *testing.T) {
	runtimeObj := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "orphaned",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}

	r, client := newTestReconciler(t,
		nil,
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": runtimeObj},
	)

	err := r.reconcileObject(context.Background(), "policy-1")
	assert.NoError(t, err)
	assert.Equal(t, 0, client.applyCalls, "Should not apply orphaned object")
	assert.Equal(t, 1, client.deleteCalls, "Should delete orphaned runtime object")
}

func TestReconcileAll_SkipsUnchangedObjects(t *testing.T) {
	unchanged := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "unchanged",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}
	changed := &pb.ConfiguredKubernetesObjectData{
		Id:          "policy-2",
		Name:        "changed",
		Annotations: map[string]string{"note": "new"},
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}
	changedRuntime := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-2",
		Name: "changed",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}

	r, client := newTestReconciler(t,
		map[string]*pb.ConfiguredKubernetesObjectData{
			"policy-1": unchanged,
			"policy-2": changed,
		},
		map[string]*pb.ConfiguredKubernetesObjectData{
			"policy-1": unchanged,
			"policy-2": changedRuntime,
		},
	)

	err := r.reconcileAll(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, 1, client.applyCalls, "Should only apply the changed object, not the unchanged one")
}

func TestReconcileObject_AppliesNewObject(t *testing.T) {
	configObj := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "new-policy",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}

	r, client := newTestReconciler(t,
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": configObj},
		nil,
	)

	err := r.reconcileObject(context.Background(), "policy-1")
	assert.NoError(t, err)
	assert.Equal(t, 1, client.applyCalls, "Should apply when object exists only in config")
}

func TestReconcileObject_NoOpWhenNotInEitherCache(t *testing.T) {
	r, client := newTestReconciler(t, nil, nil)

	err := r.reconcileObject(context.Background(), "non-existent")
	assert.NoError(t, err)
	assert.Equal(t, 0, client.applyCalls)
	assert.Equal(t, 0, client.deleteCalls)
}

func TestReconcileObject_ApplyError(t *testing.T) {
	configObj := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "allow-web",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}

	r, client := newTestReconciler(t,
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": configObj},
		nil,
	)

	client.applyErr = fmt.Errorf("API server unavailable")

	err := r.reconcileObject(context.Background(), "policy-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to apply")
	assert.Equal(t, 1, client.applyCalls)
}

func TestReconcileObject_DeleteError(t *testing.T) {
	runtimeObj := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "orphaned",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}

	r, client := newTestReconciler(t,
		nil,
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": runtimeObj},
	)

	client.deleteErr = fmt.Errorf("permission denied")

	err := r.reconcileObject(context.Background(), "policy-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete")
	assert.Equal(t, 1, client.deleteCalls)
}

func TestReconcileObject_DeleteNotFoundIsNotError(t *testing.T) {
	runtimeObj := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "already-gone",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}

	r, client := newTestReconciler(t,
		nil,
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": runtimeObj},
	)

	client.deleteErr = apierrors.NewNotFound(
		schema.GroupResource{Group: "cilium.io", Resource: "ciliumnetworkpolicies"},
		"already-gone",
	)

	err := r.reconcileObject(context.Background(), "policy-1")
	assert.NoError(t, err, "NotFound on delete should not be an error")
	assert.Equal(t, 1, client.deleteCalls)
}

func TestReconcileObject_ApplyErrorUnsupportedKind(t *testing.T) {
	configObj := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "bad-kind",
		// No KindSpecific set — unsupported
	}

	r, client := newTestReconciler(t,
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": configObj},
		nil,
	)

	err := r.reconcileObject(context.Background(), "policy-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported kind_specific")
	assert.Equal(t, 0, client.applyCalls, "Should not reach ApplyResource")
}

func TestReconcileObject_DeleteErrorUnsupportedKind(t *testing.T) {
	runtimeObj := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "bad-kind",
		// No KindSpecific set — unsupported
	}

	r, client := newTestReconciler(t,
		nil,
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": runtimeObj},
	)

	err := r.reconcileObject(context.Background(), "policy-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported kind_specific")
	assert.Equal(t, 0, client.deleteCalls, "Should not reach DeleteResource")
}

func TestReconcileAll_CollectsErrors(t *testing.T) {
	obj1 := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "good-policy",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}
	obj2 := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-2",
		Name: "bad-kind",
		// No KindSpecific — will fail
	}

	r, client := newTestReconciler(t,
		map[string]*pb.ConfiguredKubernetesObjectData{
			"policy-1": obj1,
			"policy-2": obj2,
		},
		nil,
	)

	err := r.reconcileAll(context.Background())
	assert.Error(t, err, "Should return error from the failed object")
	assert.Equal(t, 1, client.applyCalls, "Should still apply the valid object")
}

func TestReconcileAll_AppliesAndDeletes(t *testing.T) {
	configObj := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "desired",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}
	orphanedObj := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-2",
		Name: "orphaned",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}

	r, client := newTestReconciler(t,
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": configObj},
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-2": orphanedObj},
	)

	err := r.reconcileAll(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, 1, client.applyCalls, "Should apply the config-only object")
	assert.Equal(t, 1, client.deleteCalls, "Should delete the runtime-only object")
}

// drainAndReconcile reads all notifications from a cache channel and feeds each one
// into the reconciler's processResourceChange. This simulates the reconciler's select
// loop in production: cache notification → reconcileObject → API call (or skip).
// It exits when the channel is closed.
func drainAndReconcile(r *Reconciler, ch <-chan string) {
	ctx := context.Background()
	reconcileTimer := time.NewTimer(FullReconcileInterval)

	defer reconcileTimer.Stop()

	for id := range ch {
		r.processResourceChange(ctx, id, reconcileTimer)
	}
}

// liveReconcilerEnv holds live caches and a reconciler for integration tests.
// The caches remain open so tests can Insert after setup and verify
// whether notifications flow through to API calls.
type liveReconcilerEnv struct {
	client       *mockClient
	configCache  *cache.ConfiguredObjectCache
	runtimeCache *cache.ConfiguredObjectCache
	reconciler   *Reconciler
}

// newLiveReconcilerEnv creates caches, populates them with initial snapshots,
// drains the snapshot notifications, and wires up a reconciler.
func newLiveReconcilerEnv(t *testing.T, configObjects, runtimeObjects map[string]*pb.ConfiguredKubernetesObjectData) *liveReconcilerEnv {
	t.Helper()

	client := newMockClient()
	configCache := cache.NewConfiguredObjectCache()
	runtimeCache := cache.NewConfiguredObjectCache()

	if configObjects == nil {
		configObjects = make(map[string]*pb.ConfiguredKubernetesObjectData)
	}

	if runtimeObjects == nil {
		runtimeObjects = make(map[string]*pb.ConfiguredKubernetesObjectData)
	}

	go configCache.ReplaceAll(configObjects)
	<-configCache.ResourceChanged()

	go runtimeCache.ReplaceAll(runtimeObjects)
	<-runtimeCache.ResourceChanged()

	r := NewReconciler(zap.NewNop(), client, configCache, runtimeCache)
	r.resourceInfo = map[string]resources.ResourceInfo{
		"ciliumnetworkpolicies":            {Group: "cilium.io", Version: "v2"},
		"ciliumclusterwidenetworkpolicies": {Group: "cilium.io", Version: "v2"},
		"ciliumcidrgroups":                 {Group: "cilium.io", Version: "v2alpha1"},
	}

	return &liveReconcilerEnv{
		client:       client,
		configCache:  configCache,
		runtimeCache: runtimeCache,
		reconciler:   r,
	}
}

// drainCache spawns a goroutine that reads notifications from the given cache
// and feeds each one into the reconciler. Returns a done channel that closes
// when the cache is closed and all notifications are processed.
func (e *liveReconcilerEnv) drainCache(targetCache *cache.ConfiguredObjectCache) chan struct{} {
	done := make(chan struct{})

	go func() {
		drainAndReconcile(e.reconciler, targetCache.ResourceChanged())
		close(done)
	}()

	return done
}

func TestLiveReconciler_CacheDeduplicatesIdenticalInsert(t *testing.T) {
	obj := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "allow-web",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}

	env := newLiveReconcilerEnv(t,
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": obj},
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": obj},
	)
	done := env.drainCache(env.runtimeCache)

	// Simulate watcher re-delivering the same runtime object (e.g., status update).
	// Cache proto.Equal sees old == new → skips notification → no API call.
	env.runtimeCache.Insert("policy-1", &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "allow-web",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	})

	env.runtimeCache.Close()
	<-done

	assert.Equal(t, 0, env.client.applyCalls, "Identical runtime insert should not trigger an apply")
}

func TestLiveReconciler_RuntimeChangeTriggersReconcile(t *testing.T) {
	configObj := &pb.ConfiguredKubernetesObjectData{
		Id:          "policy-1",
		Name:        "allow-web",
		Annotations: map[string]string{"note": "desired"},
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}
	runtimeObj := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "allow-web",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}

	env := newLiveReconcilerEnv(t,
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": configObj},
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": runtimeObj},
	)
	done := env.drainCache(env.runtimeCache)

	// Watcher updates runtime with a different annotation — cache sees a real change → notifies.
	// Reconciler compares config vs runtime → different → applies.
	env.runtimeCache.Insert("policy-1", &pb.ConfiguredKubernetesObjectData{
		Id:          "policy-1",
		Name:        "allow-web",
		Annotations: map[string]string{"note": "stale"},
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	})

	env.runtimeCache.Close()
	<-done

	assert.Equal(t, 1, env.client.applyCalls, "Runtime change that differs from config should trigger one apply")
}

func TestLiveReconciler_ConfigChangeTriggersApply(t *testing.T) {
	obj := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "allow-web",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}

	env := newLiveReconcilerEnv(t,
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": obj},
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": obj},
	)
	done := env.drainCache(env.configCache)

	// CloudSecure sends updated config with a new annotation → cache notifies → reconciler applies.
	env.configCache.Insert("policy-1", &pb.ConfiguredKubernetesObjectData{
		Id:          "policy-1",
		Name:        "allow-web",
		Annotations: map[string]string{"note": "new"},
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	})

	env.configCache.Close()
	<-done

	assert.Equal(t, 1, env.client.applyCalls, "Config change should trigger one apply")
}

func TestLiveReconciler_NewConfigObjectTriggersApply(t *testing.T) {
	env := newLiveReconcilerEnv(t, nil, nil)
	done := env.drainCache(env.configCache)

	// CloudSecure sends a brand new policy → new to cache → notifies → reconciler applies.
	env.configCache.Insert("policy-1", &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "new-policy",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	})

	env.configCache.Close()
	<-done

	assert.Equal(t, 1, env.client.applyCalls, "New config object should trigger one apply")
	assert.Equal(t, 0, env.client.deleteCalls)
}

func TestLiveReconciler_OrphanedRuntimeObjectTriggersDelete(t *testing.T) {
	env := newLiveReconcilerEnv(t, nil, nil)
	done := env.drainCache(env.runtimeCache)

	// Watcher discovers a managed object not in config → notifies → reconciler deletes.
	env.runtimeCache.Insert("policy-1", &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "orphaned",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	})

	env.runtimeCache.Close()
	<-done

	assert.Equal(t, 0, env.client.applyCalls)
	assert.Equal(t, 1, env.client.deleteCalls, "Orphaned runtime object should trigger one delete")
}

func TestLiveReconciler_AnnotationDeletionTriggersApply(t *testing.T) {
	obj := &pb.ConfiguredKubernetesObjectData{
		Id:          "policy-1",
		Name:        "allow-web",
		Annotations: map[string]string{"note": "keep-this"},
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}

	env := newLiveReconcilerEnv(t,
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": obj},
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": obj},
	)
	done := env.drainCache(env.configCache)

	// CloudSecure removes the annotation → config changed → cache notifies.
	// Reconciler compares config (no annotation) vs runtime (has annotation) → different → applies.
	// SSA will remove the annotation because cloud-operator owned it.
	env.configCache.Insert("policy-1", &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "allow-web",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	})

	env.configCache.Close()
	<-done

	assert.Equal(t, 1, env.client.applyCalls, "Annotation deletion should trigger exactly one apply")
	assert.Equal(t, 0, env.client.deleteCalls)
}
