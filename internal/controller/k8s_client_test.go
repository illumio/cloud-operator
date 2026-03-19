// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

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
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func TestNewRealKubernetesClientFromClients(t *testing.T) {
	fakeClientset := k8sfake.NewSimpleClientset()
	scheme := runtime.NewScheme()
	fakeDynamic := fake.NewSimpleDynamicClient(scheme)

	client := NewRealKubernetesClientFromClients(fakeClientset, fakeDynamic)

	assert.NotNil(t, client)
	assert.NotNil(t, client.GetClientset())
	assert.NotNil(t, client.GetDynamicClient())
}

func TestRealKubernetesClient_GetClientset(t *testing.T) {
	fakeClientset := k8sfake.NewSimpleClientset()
	client := NewRealKubernetesClientFromClients(fakeClientset, nil)

	result := client.GetClientset()

	assert.Equal(t, fakeClientset, result)
}

func TestRealKubernetesClient_GetDiscoveryClient(t *testing.T) {
	fakeClientset := k8sfake.NewSimpleClientset()
	client := NewRealKubernetesClientFromClients(fakeClientset, nil)

	discovery := client.GetDiscoveryClient()

	assert.NotNil(t, discovery)
}

func TestRealKubernetesClient_GetSecret(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"key": []byte("value"),
		},
	}
	fakeClientset := k8sfake.NewSimpleClientset(secret)
	client := NewRealKubernetesClientFromClients(fakeClientset, nil)

	result, err := client.GetSecret(context.Background(), "default", "test-secret")

	require.NoError(t, err)
	assert.Equal(t, "test-secret", result.Name)
	assert.Equal(t, []byte("value"), result.Data["key"])
}

func TestRealKubernetesClient_GetSecret_NotFound(t *testing.T) {
	fakeClientset := k8sfake.NewSimpleClientset()
	client := NewRealKubernetesClientFromClients(fakeClientset, nil)

	_, err := client.GetSecret(context.Background(), "default", "nonexistent")

	assert.Error(t, err)
}

func TestRealKubernetesClient_CreateSecret(t *testing.T) {
	fakeClientset := k8sfake.NewSimpleClientset()
	client := NewRealKubernetesClientFromClients(fakeClientset, nil)

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

func TestRealKubernetesClient_UpdateSecret(t *testing.T) {
	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"key": []byte("old-value"),
		},
	}
	fakeClientset := k8sfake.NewSimpleClientset(existingSecret)
	client := NewRealKubernetesClientFromClients(fakeClientset, nil)

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

func TestRealKubernetesClient_ListResources(t *testing.T) {
	// Create a fake dynamic client with custom list kinds for pods
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	fakeDynamic := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			gvr: "PodList",
		},
	)
	fakeClientset := k8sfake.NewSimpleClientset()
	client := NewRealKubernetesClientFromClients(fakeClientset, fakeDynamic)

	result, err := client.ListResources(context.Background(), gvr, "default")

	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestRealKubernetesClient_ListResources_ClusterScoped(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}
	fakeDynamic := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			gvr: "NodeList",
		},
	)
	fakeClientset := k8sfake.NewSimpleClientset()
	client := NewRealKubernetesClientFromClients(fakeClientset, fakeDynamic)

	result, err := client.ListResources(context.Background(), gvr, "")

	require.NoError(t, err)
	assert.NotNil(t, result)
}

// Verify the interface is properly implemented.
func TestRealKubernetesClient_ImplementsInterface(t *testing.T) {
	fakeClientset := k8sfake.NewSimpleClientset()
	fakeDynamic := fake.NewSimpleDynamicClient(runtime.NewScheme())

	var _ = NewRealKubernetesClientFromClients(fakeClientset, fakeDynamic)
}

// Test with nil dynamic client (edge case).
func TestRealKubernetesClient_GetDynamicClient_Nil(t *testing.T) {
	fakeClientset := k8sfake.NewSimpleClientset()
	client := NewRealKubernetesClientFromClients(fakeClientset, nil)

	result := client.GetDynamicClient()

	assert.Nil(t, result)
}

// Helper to get kubernetes.Interface for type checking.
func getClientsetInterface(client KubernetesClient) kubernetes.Interface {
	return client.GetClientset()
}

func TestRealKubernetesClient_GetClientset_ReturnsInterface(t *testing.T) {
	fakeClientset := k8sfake.NewSimpleClientset()
	client := NewRealKubernetesClientFromClients(fakeClientset, nil)

	result := getClientsetInterface(client)

	assert.NotNil(t, result)
}
