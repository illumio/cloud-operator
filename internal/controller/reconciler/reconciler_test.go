// Copyright 2026 Illumio, Inc. All Rights Reserved.

package reconciler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	ctx := context.Background()
	logger := zap.NewNop()
	client := newMockClient()
	configCache := cache.NewConfiguredObjectCache()
	runtimeCache := cache.NewConfiguredObjectCache()

	// Mark caches as ready with empty data
	go func() {
		err := configCache.ReplaceAll(ctx, make(map[string]*pb.ConfiguredKubernetesObjectData))

		assert.NoError(t, err)
	}()

	<-configCache.ResourceChanged()

	go func() {
		err := runtimeCache.ReplaceAll(ctx, make(map[string]*pb.ConfiguredKubernetesObjectData))

		assert.NoError(t, err)
	}()

	<-runtimeCache.ResourceChanged()

	r := NewReconciler(logger, client, configCache, runtimeCache)
	// Set resourceInfo for test (normally done in Start())
	r.resourceInfo = map[string]resources.ResourceInfo{
		"ciliumnetworkpolicies": {Group: "cilium.io", Version: "v2"},
	}

	err := r.reconcileAll(ctx)
	require.NoError(t, err)
}

// populateCache fills a cache and drains its notifications in the background.
// The cache remains open so that subsequent operations (e.g. Delete) can still
// send notifications without panicking on a closed channel.
func populateCache(t *testing.T, c *cache.ConfiguredObjectCache, objects map[string]*pb.ConfiguredKubernetesObjectData) {
	t.Helper()

	// Start draining before ReplaceAll so the notification channel doesn't block.
	go func() {
		for range c.ResourceChanged() {
		}
	}()

	if objects != nil {
		err := c.ReplaceAll(context.Background(), objects)

		require.NoError(t, err)
	}

	t.Cleanup(func() {
		c.Close()
	})
}

// newTestReconciler creates a reconciler with pre-populated caches for testing.
func newTestReconciler(t *testing.T, configObjects, runtimeObjects map[string]*pb.ConfiguredKubernetesObjectData) (*Reconciler, *mockClient) {
	t.Helper()

	client := newMockClient()
	configCache := cache.NewConfiguredObjectCache()
	runtimeCache := cache.NewConfiguredObjectCache()

	populateCache(t, configCache, configObjects)
	populateCache(t, runtimeCache, runtimeObjects)

	r := NewReconciler(zap.NewNop(), client, configCache, runtimeCache)
	r.resourceInfo = map[string]resources.ResourceInfo{
		"ciliumnetworkpolicies":            {Group: "cilium.io", Version: "v2"},
		"ciliumclusterwidenetworkpolicies": {Group: "cilium.io", Version: "v2"},
		"ciliumcidrgroups":                 {Group: "cilium.io", Version: "v2alpha1"},
	}

	return r, client
}

func TestReconcileObject_SkipsApplyWhenMatching(t *testing.T) {
	configObj := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "allow-web",
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

	r, client := newTestReconciler(t,
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": configObj},
		map[string]*pb.ConfiguredKubernetesObjectData{"policy-1": runtimeObj},
	)

	err := r.reconcileObject(context.Background(), "policy-1")
	require.NoError(t, err)
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
	require.NoError(t, err)
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
	require.NoError(t, err)
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
	require.NoError(t, err)
	assert.Equal(t, 0, client.applyCalls, "Should not apply orphaned object")
	assert.Equal(t, 1, client.deleteCalls, "Should delete orphaned runtime object")
}

func TestReconcileAll_SkipsUnchangedObjects(t *testing.T) {
	unchangedConfig := &pb.ConfiguredKubernetesObjectData{
		Id:   "policy-1",
		Name: "unchanged",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}
	unchangedRuntime := &pb.ConfiguredKubernetesObjectData{
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
			"policy-1": unchangedConfig,
			"policy-2": changed,
		},
		map[string]*pb.ConfiguredKubernetesObjectData{
			"policy-1": unchangedRuntime,
			"policy-2": changedRuntime,
		},
	)

	err := r.reconcileAll(context.Background())
	require.NoError(t, err)
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
	require.NoError(t, err)
	assert.Equal(t, 1, client.applyCalls, "Should apply when object exists only in config")
}

func TestReconcileObject_NoOpWhenNotInEitherCache(t *testing.T) {
	r, client := newTestReconciler(t, nil, nil)

	err := r.reconcileObject(context.Background(), "non-existent")
	require.NoError(t, err)
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

	client.applyErr = errors.New("API server unavailable")

	err := r.reconcileObject(context.Background(), "policy-1")
	require.Error(t, err)
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

	client.deleteErr = errors.New("permission denied")

	err := r.reconcileObject(context.Background(), "policy-1")
	require.Error(t, err)
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
	require.NoError(t, err, "NotFound on delete should not be an error")
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
	require.Error(t, err)
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
	require.Error(t, err)
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
	require.Error(t, err, "Should return error from the failed object")
	assert.Equal(t, 1, client.applyCalls, "Should still apply the valid object")
}

// TestContextCancelDuringWaitForCaches verifies that cancelling the context
// while the reconciler is blocked in waitForCaches (neither cache ready) causes Run()
// to return promptly without deadlocking.
func TestContextCancelDuringWaitForCaches(t *testing.T) {
	client := newMockClient()
	configCache := cache.NewConfiguredObjectCache()
	runtimeCache := cache.NewConfiguredObjectCache()

	t.Cleanup(func() {
		configCache.Close()
		runtimeCache.Close()
	})

	r := NewReconciler(zap.NewNop(), client, configCache, runtimeCache)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})

	go func() {
		r.Run(ctx)
		close(done)
	}()

	// Cancel context while reconciler is stuck in waitForCaches (no snapshots sent)
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Run() returned — no deadlock
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after context cancellation — likely deadlocked in waitForCaches")
	}
}

// TestCacheCloseUnblocksReconcilerLoop verifies that closing the cache
// (which closes the resourceChanged channel) causes the reconciler's select loop
// to exit gracefully when combined with context cancellation.
func TestCacheCloseUnblocksReconcilerLoop(t *testing.T) {
	client := newMockClient()
	configCache := cache.NewConfiguredObjectCache()
	runtimeCache := cache.NewConfiguredObjectCache()

	r := NewReconciler(zap.NewNop(), client, configCache, runtimeCache)
	// Pre-set resourceInfo so Run() skips the discovery retry loop entirely
	r.resourceInfo = map[string]resources.ResourceInfo{
		"ciliumnetworkpolicies": {Group: "cilium.io", Version: "v2"},
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Mark both caches ready so reconciler gets past waitForCaches.
	// We drain the SnapshotReplaced notifications that ReplaceAll sends.
	go func() {
		<-configCache.ResourceChanged()
		<-runtimeCache.ResourceChanged()
	}()

	require.NoError(t, configCache.ReplaceAll(ctx, map[string]*pb.ConfiguredKubernetesObjectData{}))
	require.NoError(t, runtimeCache.ReplaceAll(ctx, map[string]*pb.ConfiguredKubernetesObjectData{}))

	done := make(chan struct{})

	go func() {
		// waitForCaches uses Run internally — but we pre-set resourceInfo,
		// so call waitForCaches + the main loop directly via the exported Run.
		// Since resourceInfo is set, Run skips discovery and goes straight to waitForCaches.
		_ = r.waitForCaches(ctx)

		close(done)
	}()

	// waitForCaches should return immediately since both caches are ready
	select {
	case <-done:
		// good — caches were ready
	case <-time.After(5 * time.Second):
		t.Fatal("waitForCaches did not return — likely deadlocked")
	}

	// Now test the actual reconciler loop shutdown
	done = make(chan struct{})

	go func() {
		r.Run(ctx)
		close(done)
	}()

	// Give reconciler time to enter the main select loop
	// (it just needs to get past waitForCaches + initial reconcileAll, both of which
	// are near-instant with empty caches and pre-set resourceInfo)
	time.Sleep(200 * time.Millisecond)

	// Close caches and cancel context — reconciler should exit
	configCache.Close()
	runtimeCache.Close()
	cancel()

	select {
	case <-done:
		// Run() returned — no deadlock
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after cache close + context cancel — likely deadlocked")
	}
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
	require.NoError(t, err)
	assert.Equal(t, 1, client.applyCalls, "Should apply the config-only object")
	assert.Equal(t, 1, client.deleteCalls, "Should delete the runtime-only object")
}
