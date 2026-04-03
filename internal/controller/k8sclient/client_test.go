// Copyright 2026 Illumio, Inc. All Rights Reserved.

package k8sclient

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func TestNewClientFromClients(t *testing.T) {
	fakeClientset := k8sfake.NewClientset()
	scheme := runtime.NewScheme()
	fakeDynamic := fake.NewSimpleDynamicClient(scheme)

	client := NewClientFromClients(fakeClientset, fakeDynamic)

	assert.NotNil(t, client)
	assert.NotNil(t, client.GetClientset())
	assert.NotNil(t, client.GetDynamicClient())
}

func TestClient_GetClientset(t *testing.T) {
	fakeClientset := k8sfake.NewClientset()
	client := NewClientFromClients(fakeClientset, nil)

	result := client.GetClientset()

	assert.Equal(t, fakeClientset, result)
}

func TestClient_GetDiscoveryClient(t *testing.T) {
	fakeClientset := k8sfake.NewClientset()
	client := NewClientFromClients(fakeClientset, nil)

	discovery := client.GetDiscoveryClient()

	assert.NotNil(t, discovery)
}

func TestClient_GetSecret(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"key": []byte("value"),
		},
	}
	fakeClientset := k8sfake.NewClientset(secret)
	client := NewClientFromClients(fakeClientset, nil)

	result, err := client.GetSecret(context.Background(), "default", "test-secret")

	require.NoError(t, err)
	assert.Equal(t, "test-secret", result.Name)
	assert.Equal(t, []byte("value"), result.Data["key"])
}

func TestClient_GetSecret_NotFound(t *testing.T) {
	fakeClientset := k8sfake.NewClientset()
	client := NewClientFromClients(fakeClientset, nil)

	_, err := client.GetSecret(context.Background(), "default", "nonexistent")

	assert.Error(t, err)
}

func TestClient_CreateSecret(t *testing.T) {
	fakeClientset := k8sfake.NewClientset()
	client := NewClientFromClients(fakeClientset, nil)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "new-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"key": []byte("value"),
		},
	}

	result, err := client.CreateSecret(context.Background(), "default", secret)

	require.NoError(t, err)
	assert.Equal(t, "new-secret", result.Name)

	// Verify it was actually created
	retrieved, err := client.GetSecret(context.Background(), "default", "new-secret")
	require.NoError(t, err)
	assert.Equal(t, "new-secret", retrieved.Name)
}

func TestClient_UpdateSecret(t *testing.T) {
	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"key": []byte("old-value"),
		},
	}
	fakeClientset := k8sfake.NewClientset(existingSecret)
	client := NewClientFromClients(fakeClientset, nil)

	updatedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"key": []byte("new-value"),
		},
	}

	result, err := client.UpdateSecret(context.Background(), "default", updatedSecret)

	require.NoError(t, err)
	assert.Equal(t, []byte("new-value"), result.Data["key"])
}

func TestClient_ListResources(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	fakeDynamic := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			gvr: "PodList",
		},
	)
	fakeClientset := k8sfake.NewClientset()
	client := NewClientFromClients(fakeClientset, fakeDynamic)

	result, err := client.ListResources(context.Background(), gvr, "default")

	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestClient_ListResources_ClusterScoped(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}
	fakeDynamic := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			gvr: "NodeList",
		},
	)
	fakeClientset := k8sfake.NewClientset()
	client := NewClientFromClients(fakeClientset, fakeDynamic)

	result, err := client.ListResources(context.Background(), gvr, "")

	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestClient_GetDynamicClient_Nil(t *testing.T) {
	fakeClientset := k8sfake.NewClientset()
	client := NewClientFromClients(fakeClientset, nil)

	result := client.GetDynamicClient()

	assert.Nil(t, result)
}

func TestIsRunningInCluster(t *testing.T) {
	// This test just verifies the function doesn't panic
	// Actual behavior depends on environment
	_ = IsRunningInCluster()
}

func TestClient_WatchResources_Namespaced(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	fakeDynamic := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			gvr: "PodList",
		},
	)
	fakeClientset := k8sfake.NewSimpleClientset()
	client := NewClientFromClients(fakeClientset, fakeDynamic)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher, err := client.WatchResources(ctx, gvr, "default", "")

	require.NoError(t, err)
	assert.NotNil(t, watcher)

	// Clean up
	watcher.Stop()
}

func TestClient_WatchResources_ClusterScoped(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}
	fakeDynamic := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			gvr: "NodeList",
		},
	)
	fakeClientset := k8sfake.NewSimpleClientset()
	client := NewClientFromClients(fakeClientset, fakeDynamic)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher, err := client.WatchResources(ctx, gvr, "", "")

	require.NoError(t, err)
	assert.NotNil(t, watcher)

	// Clean up
	watcher.Stop()
}

func TestClient_WatchResources_WithResourceVersion(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	fakeDynamic := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			gvr: "PodList",
		},
	)
	fakeClientset := k8sfake.NewSimpleClientset()
	client := NewClientFromClients(fakeClientset, fakeDynamic)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher, err := client.WatchResources(ctx, gvr, "default", "12345")

	require.NoError(t, err)
	assert.NotNil(t, watcher)

	// Clean up
	watcher.Stop()
}

func TestIsRunningInCluster_NotInCluster(t *testing.T) {
	// t.Setenv automatically restores the original value after the test
	t.Setenv("KUBERNETES_SERVICE_HOST", "")

	result := IsRunningInCluster()
	assert.False(t, result)
}

func TestIsRunningInCluster_InCluster(t *testing.T) {
	// t.Setenv automatically restores the original value after the test
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")

	result := IsRunningInCluster()
	assert.True(t, result)
}
