// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakediscovery "k8s.io/client-go/discovery/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/illumio/cloud-operator/internal/controller/stream"
)

func TestBuildResourceApiGroupMap(t *testing.T) {
	logger := zap.NewNop()

	t.Run("maps resources to API groups", func(t *testing.T) {
		clientset := k8sfake.NewSimpleClientset()

		fakeDiscovery, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
		require.True(t, ok, "failed to get fake discovery client")

		fakeDiscovery.Resources = []*metav1.APIResourceList{
			{
				GroupVersion: "v1",
				APIResources: []metav1.APIResource{
					{Name: "pods", Kind: "Pod"},
					{Name: "services", Kind: "Service"},
				},
			},
			{
				GroupVersion: "apps/v1",
				APIResources: []metav1.APIResource{
					{Name: "deployments", Kind: "Deployment"},
				},
			},
		}

		resources := []string{"pods", "deployments"}

		result, err := buildResourceApiGroupMap(resources, clientset, logger)
		require.NoError(t, err)

		// pods should be in core group (empty string)
		apiGroup, ok := result["pods"]
		assert.True(t, ok, "expected 'pods' to be in result")
		assert.Empty(t, apiGroup, "expected pods apiGroup to be empty")
	})

	t.Run("handles empty resources", func(t *testing.T) {
		clientset := k8sfake.NewSimpleClientset()

		resources := []string{}

		result, err := buildResourceApiGroupMap(resources, clientset, logger)
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("skips metrics.k8s.io group", func(t *testing.T) {
		clientset := k8sfake.NewSimpleClientset()

		fakeDiscovery, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
		require.True(t, ok, "failed to get fake discovery client")

		fakeDiscovery.Resources = []*metav1.APIResourceList{
			{
				GroupVersion: "metrics.k8s.io/v1beta1",
				APIResources: []metav1.APIResource{
					{Name: "nodes", Kind: "NodeMetrics"},
				},
			},
		}

		resources := []string{"nodes"}

		result, err := buildResourceApiGroupMap(resources, clientset, logger)
		require.NoError(t, err)

		// nodes should NOT be mapped because metrics.k8s.io is skipped
		_, ok = result["nodes"]
		assert.False(t, ok, "expected 'nodes' to be skipped for metrics.k8s.io group")
	})

	t.Run("handles apps group resources", func(t *testing.T) {
		clientset := k8sfake.NewSimpleClientset()

		fakeDiscovery, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
		require.True(t, ok, "failed to get fake discovery client")

		fakeDiscovery.Resources = []*metav1.APIResourceList{
			{
				GroupVersion: "apps/v1",
				APIResources: []metav1.APIResource{
					{Name: "deployments", Kind: "Deployment"},
					{Name: "statefulsets", Kind: "StatefulSet"},
					{Name: "daemonsets", Kind: "DaemonSet"},
				},
			},
		}

		resources := []string{"deployments", "statefulsets"}

		result, err := buildResourceApiGroupMap(resources, clientset, logger)
		require.NoError(t, err)

		assert.Equal(t, "apps", result["deployments"])
		assert.Equal(t, "apps", result["statefulsets"])
	})
}

func TestSetProcessingResources_Integration(t *testing.T) {
	// Reset state
	stream.SetProcessingResources(false)

	t.Run("sets processing to true and server is healthy", func(t *testing.T) {
		stream.SetProcessingResources(true)
		assert.True(t, stream.ServerIsHealthy())
		stream.SetProcessingResources(false)
	})

	t.Run("sets processing to false and server is healthy", func(t *testing.T) {
		stream.SetProcessingResources(false)
		assert.True(t, stream.ServerIsHealthy())
	})
}

func TestGetVersionForGroup(t *testing.T) {
	tests := []struct {
		name     string
		group    string
		expected string
	}{
		{
			name:     "core API group returns v1",
			group:    "",
			expected: "v1",
		},
		{
			name:     "apps group returns v1",
			group:    "apps",
			expected: "v1",
		},
		{
			name:     "networking.k8s.io group returns v1",
			group:    "networking.k8s.io",
			expected: "v1",
		},
		{
			name:     "cilium.io group returns v2",
			group:    "cilium.io",
			expected: "v2",
		},
		{
			name:     "networking.k8s.aws group returns v1alpha1",
			group:    "networking.k8s.aws",
			expected: "v1alpha1",
		},
		{
			name:     "unknown group returns v1",
			group:    "unknown.io",
			expected: "v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getVersionForGroup(tt.group)
			if got != tt.expected {
				t.Errorf("getVersionForGroup(%q) = %q, want %q", tt.group, got, tt.expected)
			}
		})
	}
}
