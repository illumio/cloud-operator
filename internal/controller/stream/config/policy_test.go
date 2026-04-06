// Copyright 2026 Illumio, Inc. All Rights Reserved.

package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

func setupTestGVRCache() {
	policyGVRCache["CiliumNetworkPolicy"] = schema.GroupVersionResource{
		Group:    "cilium.io",
		Version:  "v2",
		Resource: "ciliumnetworkpolicies",
	}
	policyGVRCache["NetworkPolicy"] = schema.GroupVersionResource{
		Group:    "networking.k8s.io",
		Version:  "v1",
		Resource: "networkpolicies",
	}
}

func TestGetGVR(t *testing.T) {
	setupTestGVRCache()

	tests := []struct {
		name    string
		kind    string
		wantGVR schema.GroupVersionResource
		wantErr bool
	}{
		{
			name: "cilium network policy",
			kind: "CiliumNetworkPolicy",
			wantGVR: schema.GroupVersionResource{
				Group:    "cilium.io",
				Version:  "v2",
				Resource: "ciliumnetworkpolicies",
			},
			wantErr: false,
		},
		{
			name: "k8s network policy",
			kind: "NetworkPolicy",
			wantGVR: schema.GroupVersionResource{
				Group:    "networking.k8s.io",
				Version:  "v1",
				Resource: "networkpolicies",
			},
			wantErr: false,
		},
		{
			name:    "unknown kind",
			kind:    "UnknownKind",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gvr, err := getGVR(tt.kind)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantGVR, gvr)
			}
		})
	}
}

func TestCreatePolicy(t *testing.T) {
	setupTestGVRCache()

	gvr := schema.GroupVersionResource{
		Group:    "cilium.io",
		Version:  "v2",
		Resource: "ciliumnetworkpolicies",
	}

	scheme := runtime.NewScheme()
	fakeDynamic := fake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			gvr: "CiliumNetworkPolicyList",
		},
	)

	logger := zap.NewNop()
	ctx := context.Background()

	policyJSON := []byte(`{
		"apiVersion": "cilium.io/v2",
		"kind": "CiliumNetworkPolicy",
		"metadata": {
			"name": "test-policy",
			"namespace": "default"
		},
		"spec": {
			"endpointSelector": {}
		}
	}`)

	policyData := &pb.NetworkPolicyData{
		Id:        "test-001",
		Name:      "test-policy",
		Namespace: "default",
		Kind:      "CiliumNetworkPolicy",
		Resource:  policyJSON,
	}

	err := CreatePolicy(ctx, fakeDynamic, logger, policyData)
	require.NoError(t, err)

	// Verify policy was created
	created, err := fakeDynamic.Resource(gvr).Namespace("default").Get(ctx, "test-policy", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "test-policy", created.GetName())
}

func TestCreatePolicy_InvalidJSON(t *testing.T) {
	setupTestGVRCache()

	scheme := runtime.NewScheme()
	fakeDynamic := fake.NewSimpleDynamicClient(scheme)
	logger := zap.NewNop()
	ctx := context.Background()

	policyData := &pb.NetworkPolicyData{
		Id:        "test-003",
		Name:      "test-policy",
		Namespace: "default",
		Kind:      "CiliumNetworkPolicy",
		Resource:  []byte(`{invalid json`),
	}

	err := CreatePolicy(ctx, fakeDynamic, logger, policyData)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestCreatePolicy_UnknownKind(t *testing.T) {
	setupTestGVRCache()

	scheme := runtime.NewScheme()
	fakeDynamic := fake.NewSimpleDynamicClient(scheme)
	logger := zap.NewNop()
	ctx := context.Background()

	policyData := &pb.NetworkPolicyData{
		Id:        "test-004",
		Name:      "test-policy",
		Namespace: "default",
		Kind:      "UnknownKind",
		Resource:  []byte(`{}`),
	}

	err := CreatePolicy(ctx, fakeDynamic, logger, policyData)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown kind")
}

func TestDeletePolicy(t *testing.T) {
	setupTestGVRCache()

	gvr := schema.GroupVersionResource{
		Group:    "cilium.io",
		Version:  "v2",
		Resource: "ciliumnetworkpolicies",
	}

	// Create existing policy
	existingPolicy := &unstructured.Unstructured{}
	existingPolicy.SetAPIVersion("cilium.io/v2")
	existingPolicy.SetKind("CiliumNetworkPolicy")
	existingPolicy.SetName("existing-policy")
	existingPolicy.SetNamespace("default")

	scheme := runtime.NewScheme()
	fakeDynamic := fake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			gvr: "CiliumNetworkPolicyList",
		},
		existingPolicy,
	)

	logger := zap.NewNop()
	ctx := context.Background()

	policyData := &pb.NetworkPolicyData{
		Id:        "test-005",
		Name:      "existing-policy",
		Namespace: "default",
		Kind:      "CiliumNetworkPolicy",
	}

	err := DeletePolicy(ctx, fakeDynamic, logger, policyData)
	require.NoError(t, err)

	// Verify policy was deleted
	_, err = fakeDynamic.Resource(gvr).Namespace("default").Get(ctx, "existing-policy", metav1.GetOptions{})
	assert.Error(t, err)
}

func TestDeletePolicy_NotFound(t *testing.T) {
	setupTestGVRCache()

	gvr := schema.GroupVersionResource{
		Group:    "cilium.io",
		Version:  "v2",
		Resource: "ciliumnetworkpolicies",
	}

	scheme := runtime.NewScheme()
	fakeDynamic := fake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			gvr: "CiliumNetworkPolicyList",
		},
	)

	logger := zap.NewNop()
	ctx := context.Background()

	policyData := &pb.NetworkPolicyData{
		Id:        "test-006",
		Name:      "nonexistent-policy",
		Namespace: "default",
		Kind:      "CiliumNetworkPolicy",
	}

	err := DeletePolicy(ctx, fakeDynamic, logger, policyData)
	assert.Error(t, err)
}
