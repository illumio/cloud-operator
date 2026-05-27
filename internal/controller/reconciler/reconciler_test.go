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
	runtimeCache := cache.NewConfiguredObjectCache()

	r := NewReconciler(logger, client, configCache, runtimeCache)

	assert.NotNil(t, r)
	assert.Equal(t, logger, r.logger)
	assert.Equal(t, client, r.client)
	assert.Equal(t, configCache, r.configCache)
	assert.Equal(t, runtimeCache, r.runtimeCache)
}

func TestFilterAnnotationsByDesired(t *testing.T) {
	tests := map[string]struct {
		runtime  map[string]string
		desired  map[string]string
		expected map[string]string
	}{
		"extra runtime annotations are stripped": {
			runtime:  map[string]string{"note": "ours", "kubectl.kubernetes.io/restartedAt": "2026-01-01"},
			desired:  map[string]string{"note": "ours"},
			expected: map[string]string{"note": "ours"},
		},
		"no desired annotations returns nil": {
			runtime:  map[string]string{"kubectl.kubernetes.io/restartedAt": "2026-01-01"},
			desired:  nil,
			expected: nil,
		},
		"no runtime annotations returns nil": {
			runtime:  nil,
			desired:  map[string]string{"note": "ours"},
			expected: nil,
		},
		"both empty returns nil": {
			runtime:  map[string]string{},
			desired:  map[string]string{},
			expected: nil,
		},
		"matching annotations are kept": {
			runtime:  map[string]string{"note": "ours", "team": "platform"},
			desired:  map[string]string{"note": "ours", "team": "platform"},
			expected: map[string]string{"note": "ours", "team": "platform"},
		},
		"no overlap returns nil": {
			runtime:  map[string]string{"added-by-other": "controller"},
			desired:  map[string]string{"note": "ours"},
			expected: nil,
		},
		"keeps runtime value when key matches but value differs": {
			runtime:  map[string]string{"note": "v1"},
			desired:  map[string]string{"note": "v2"},
			expected: map[string]string{"note": "v1"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := filterAnnotationsByDesired(tt.runtime, tt.desired)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestObjectsMatch_IgnoresExtraRuntimeAnnotations(t *testing.T) {
	logger := zap.NewNop()
	client := newMockClient()
	configCache := cache.NewConfiguredObjectCache()
	runtimeCache := cache.NewConfiguredObjectCache()
	r := NewReconciler(logger, client, configCache, runtimeCache)

	configObj := &pb.ConfiguredKubernetesObjectData{
		Id:          "cnp-1",
		Name:        "test-policy",
		Annotations: map[string]string{"note": "from-cloudsecure"},
	}

	runtimeObj := &pb.ConfiguredKubernetesObjectData{
		Id:          "cnp-1",
		Name:        "test-policy",
		Annotations: map[string]string{"note": "from-cloudsecure", "kubectl.kubernetes.io/restartedAt": "2026-01-01"},
	}

	// Should match despite extra runtime annotation
	assert.True(t, r.objectsMatch(configObj, runtimeObj))

	// Runtime annotations should be restored after comparison
	assert.Equal(t, map[string]string{
		"note":                              "from-cloudsecure",
		"kubectl.kubernetes.io/restartedAt": "2026-01-01",
	}, runtimeObj.GetAnnotations())
}

func TestObjectsMatch_DetectsDiff(t *testing.T) {
	logger := zap.NewNop()
	client := newMockClient()
	configCache := cache.NewConfiguredObjectCache()
	runtimeCache := cache.NewConfiguredObjectCache()
	r := NewReconciler(logger, client, configCache, runtimeCache)

	configObj := &pb.ConfiguredKubernetesObjectData{
		Id:          "cnp-1",
		Name:        "test-policy",
		Annotations: map[string]string{"note": "updated-value"},
	}

	runtimeObj := &pb.ConfiguredKubernetesObjectData{
		Id:          "cnp-1",
		Name:        "test-policy",
		Annotations: map[string]string{"note": "old-value"},
	}

	// Should NOT match — the annotation CloudSecure owns has a different value
	assert.False(t, r.objectsMatch(configObj, runtimeObj))
}

func TestObjectsMatch_DriftedAnnotationTriggersUpdate(t *testing.T) {
	logger := zap.NewNop()
	client := newMockClient()
	configCache := cache.NewConfiguredObjectCache()
	runtimeCache := cache.NewConfiguredObjectCache()
	r := NewReconciler(logger, client, configCache, runtimeCache)

	configObj := &pb.ConfiguredKubernetesObjectData{
		Id:          "cnp-1",
		Name:        "test-policy",
		Annotations: map[string]string{"note": "v2"},
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{EndpointSelector: &pb.LabelSelector{MatchLabels: map[string]string{"app": "web"}}},
				},
			},
		},
	}

	runtimeObj := &pb.ConfiguredKubernetesObjectData{
		Id:          "cnp-1",
		Name:        "test-policy",
		Annotations: map[string]string{"note": "v1", "kubectl.kubernetes.io/restartedAt": "2026-01-01"},
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{EndpointSelector: &pb.LabelSelector{MatchLabels: map[string]string{"app": "web"}}},
				},
			},
		},
	}

	// Objects are identical except annotation "note" drifted from "v2" to "v1".
	// The extra runtime annotation should be ignored, but the value diff should be detected.
	assert.False(t, r.objectsMatch(configObj, runtimeObj), "drifted annotation value should trigger update")

	// Runtime annotations should be restored after comparison
	assert.Equal(t, map[string]string{
		"note":                              "v1",
		"kubectl.kubernetes.io/restartedAt": "2026-01-01",
	}, runtimeObj.GetAnnotations(), "runtime annotations should be restored")

	// Fix the drift — now they should match
	runtimeObj.Annotations = map[string]string{"note": "v2", "kubectl.kubernetes.io/restartedAt": "2026-01-01"}
	assert.True(t, r.objectsMatch(configObj, runtimeObj), "should match after drift is corrected")
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
