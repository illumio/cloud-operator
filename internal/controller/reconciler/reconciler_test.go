// Copyright 2026 Illumio, Inc. All Rights Reserved.

package reconciler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
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
	return obj, nil
}

func (m *mockClient) DeleteResource(_ context.Context, _ schema.GroupVersionResource, _, _ string) error {
	return nil
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
	runtimeCache := cache.NewRuntimeCache()

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
	runtimeCache := cache.NewRuntimeCache()

	// Drain channels so ReplaceAll doesn't block
	go func() {
		<-configCache.ResourceChanged()
		<-runtimeCache.ResourceChanged()
	}()

	// Mark caches as ready with empty data
	configCache.ReplaceAll(make(map[string]*pb.ConfiguredKubernetesObjectData))
	runtimeCache.ReplaceAll(make(map[string]*pb.ConfiguredKubernetesObjectData))

	r := NewReconciler(logger, client, configCache, runtimeCache)
	// Set resourceInfo for test (normally done in Start())
	r.resourceInfo = map[string]resources.ResourceInfo{
		"ciliumnetworkpolicies": {Group: "cilium.io", Version: "v2"},
	}

	err := r.ReconcileAll(context.Background())
	assert.NoError(t, err)
}
