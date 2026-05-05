// Copyright 2026 Illumio, Inc. All Rights Reserved.

package reconciler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	return nil, nil
}

func (m *mockClient) CreateSecret(_ context.Context, _ string, _ *corev1.Secret) (*corev1.Secret, error) {
	return nil, nil
}

func (m *mockClient) UpdateSecret(_ context.Context, _ string, _ *corev1.Secret) (*corev1.Secret, error) {
	return nil, nil
}

func (m *mockClient) ListResources(_ context.Context, _ schema.GroupVersionResource, _ string) (*unstructured.UnstructuredList, error) {
	return nil, nil
}

func (m *mockClient) WatchResources(_ context.Context, _ schema.GroupVersionResource, _ string, _ string) (watch.Interface, error) {
	return nil, nil
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
	assert.NotNil(t, r.queue)
}

func TestGetGVR(t *testing.T) {
	tests := []struct {
		name        string
		configObj   *pb.ConfiguredKubernetesObjectData
		expectedGVR schema.GroupVersionResource
		expectError bool
	}{
		{
			name: "CiliumNetworkPolicy",
			configObj: &pb.ConfiguredKubernetesObjectData{
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
					CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
				},
			},
			expectedGVR: CiliumNetworkPolicyGVR,
			expectError: false,
		},
		{
			name: "CiliumClusterwideNetworkPolicy",
			configObj: &pb.ConfiguredKubernetesObjectData{
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{},
				},
			},
			expectedGVR: CiliumClusterwideNetworkPolicyGVR,
			expectError: false,
		},
		{
			name: "CiliumCIDRGroup",
			configObj: &pb.ConfiguredKubernetesObjectData{
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumCidrGroup{
					CiliumCidrGroup: &pb.KubernetesCiliumCIDRGroupData{},
				},
			},
			expectedGVR: CiliumCIDRGroupGVR,
			expectError: false,
		},
		{
			name:        "unsupported type",
			configObj:   &pb.ConfiguredKubernetesObjectData{},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gvr, err := getGVR(tt.configObj)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedGVR, gvr)
			}
		})
	}
}

func TestGetGVRFromUnstructured(t *testing.T) {
	tests := []struct {
		name        string
		obj         *unstructured.Unstructured
		expectedGVR schema.GroupVersionResource
		expectError bool
	}{
		{
			name: "CiliumNetworkPolicy",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"kind": "CiliumNetworkPolicy",
				},
			},
			expectedGVR: CiliumNetworkPolicyGVR,
			expectError: false,
		},
		{
			name: "CiliumClusterwideNetworkPolicy",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"kind": "CiliumClusterwideNetworkPolicy",
				},
			},
			expectedGVR: CiliumClusterwideNetworkPolicyGVR,
			expectError: false,
		},
		{
			name: "CiliumCIDRGroup",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"kind": "CiliumCIDRGroup",
				},
			},
			expectedGVR: CiliumCIDRGroupGVR,
			expectError: false,
		},
		{
			name: "unsupported kind",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"kind": "UnsupportedKind",
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gvr, err := getGVRFromUnstructured(tt.obj)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedGVR, gvr)
			}
		})
	}
}

func TestReconcile_EmptyCaches(t *testing.T) {
	logger := zap.NewNop()
	client := newMockClient()
	configCache := cache.NewConfiguredObjectCache()
	runtimeCache := cache.NewRuntimeCache()

	// Mark caches as ready with empty data
	configCache.ReplaceAll(make(map[string]*pb.ConfiguredKubernetesObjectData))
	runtimeCache.ReplaceAll(make(map[string]*unstructured.Unstructured))

	r := NewReconciler(logger, client, configCache, runtimeCache)

	err := r.Reconcile(context.Background())
	assert.NoError(t, err)
}
