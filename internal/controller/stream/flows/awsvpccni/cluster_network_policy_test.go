// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsvpccni

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	discoveryfake "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestIsCRDAvailable(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name            string
		serverResources map[string]*metav1.APIResourceList
		expected        bool
	}{
		{
			name: "CRD available",
			serverResources: map[string]*metav1.APIResourceList{
				ClusterNetworkPolicyAPIVersion: {
					GroupVersion: ClusterNetworkPolicyAPIVersion,
					APIResources: []metav1.APIResource{
						{Name: "clusternetworkpolicies", Kind: "ClusterNetworkPolicy"},
					},
				},
			},
			expected: true,
		},
		{
			name:            "CRD not available",
			serverResources: map[string]*metav1.APIResourceList{},
			expected:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeDiscovery := &discoveryfake.FakeDiscovery{
				Fake: &k8stesting.Fake{},
			}

			// Add resources if provided
			if len(tt.serverResources) > 0 {
				var resources []*metav1.APIResourceList
				for _, r := range tt.serverResources {
					resources = append(resources, r)
				}

				fakeDiscovery.Resources = resources
			}

			result := IsCRDAvailable(logger, fakeDiscovery)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEnsureFlowLoggingPolicy(t *testing.T) {
	logger := zap.NewNop()
	ctx := context.Background()

	// Define the GVR for the fake client
	gvr := schema.GroupVersionResource{
		Group:    "networking.k8s.aws",
		Version:  "v1alpha1",
		Resource: "clusternetworkpolicies",
	}

	t.Run("creates policy successfully", func(t *testing.T) {
		scheme := runtime.NewScheme()
		dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)

		err := EnsureFlowLoggingPolicy(ctx, logger, dynamicClient)

		require.NoError(t, err)

		// Verify the policy was created
		created, err := dynamicClient.Resource(gvr).Get(ctx, ClusterNetworkPolicyName, metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, ClusterNetworkPolicyName, created.GetName())
		assert.Equal(t, "illumio-cloud-operator", created.GetLabels()["app.kubernetes.io/managed-by"])
	})

	t.Run("handles already exists gracefully", func(t *testing.T) {
		scheme := runtime.NewScheme()
		dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)

		// Create the policy first
		err := EnsureFlowLoggingPolicy(ctx, logger, dynamicClient)
		require.NoError(t, err)

		// Create again - should handle AlreadyExists
		err = EnsureFlowLoggingPolicy(ctx, logger, dynamicClient)
		require.NoError(t, err)
	})

	t.Run("returns error on create failure", func(t *testing.T) {
		scheme := runtime.NewScheme()
		dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)

		// Add reactor to simulate error
		expectedErr := errors.New("permission denied")

		dynamicClient.PrependReactor("create", "clusternetworkpolicies", func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, expectedErr
		})

		err := EnsureFlowLoggingPolicy(ctx, logger, dynamicClient)

		require.Error(t, err)
		assert.Equal(t, expectedErr, err)
	})
}

func TestClusterNetworkPolicySpec(t *testing.T) {
	logger := zap.NewNop()
	ctx := context.Background()

	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)

	err := EnsureFlowLoggingPolicy(ctx, logger, dynamicClient)
	require.NoError(t, err)

	gvr := schema.GroupVersionResource{
		Group:    "networking.k8s.aws",
		Version:  "v1alpha1",
		Resource: "clusternetworkpolicies",
	}

	created, err := dynamicClient.Resource(gvr).Get(ctx, ClusterNetworkPolicyName, metav1.GetOptions{})
	require.NoError(t, err)

	// Verify spec structure
	spec, found, err := unstructured.NestedMap(created.Object, "spec")
	require.NoError(t, err)
	require.True(t, found)

	// Check tier
	tier, found, err := unstructured.NestedString(spec, "tier")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "Baseline", tier)

	// Check priority
	priority, found, err := unstructured.NestedInt64(spec, "priority")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, int64(1000), priority)

	// Check ingress has Pass action
	ingress, found, err := unstructured.NestedSlice(spec, "ingress")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, ingress, 1)

	ingressRule, ok := ingress[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Pass", ingressRule["action"])

	// Check egress has Pass action
	egress, found, err := unstructured.NestedSlice(spec, "egress")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, egress, 1)

	egressRule, ok := egress[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Pass", egressRule["action"])
}

func TestClusterNetworkPolicyConstants(t *testing.T) {
	assert.Equal(t, "networking.k8s.aws/v1alpha1", ClusterNetworkPolicyAPIVersion)
	assert.Equal(t, "illumio-cloud-operator-flow-logging", ClusterNetworkPolicyName)

	assert.Equal(t, "networking.k8s.aws", clusterNetworkPolicyGVR.Group)
	assert.Equal(t, "v1alpha1", clusterNetworkPolicyGVR.Version)
	assert.Equal(t, "clusternetworkpolicies", clusterNetworkPolicyGVR.Resource)
}

// TestEnsureFlowLoggingPolicy_AlreadyExistsError tests the specific AlreadyExists error handling.
func TestEnsureFlowLoggingPolicy_AlreadyExistsError(t *testing.T) {
	logger := zap.NewNop()
	ctx := context.Background()

	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)

	// Add reactor to simulate AlreadyExists error
	dynamicClient.PrependReactor("create", "clusternetworkpolicies", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewAlreadyExists(
			schema.GroupResource{Group: "networking.k8s.aws", Resource: "clusternetworkpolicies"},
			ClusterNetworkPolicyName,
		)
	})

	err := EnsureFlowLoggingPolicy(ctx, logger, dynamicClient)

	// Should return nil (handled gracefully)
	require.NoError(t, err)
}
