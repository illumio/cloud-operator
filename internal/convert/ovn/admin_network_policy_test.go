// Copyright 2026 Illumio, Inc. All Rights Reserved.

package ovn

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestIsAdminNetworkPolicyResource(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"AdminNetworkPolicy kind", "AdminNetworkPolicy", true},
		{"BaselineAdminNetworkPolicy kind", "BaselineAdminNetworkPolicy", true},
		{"adminnetworkpolicies resource", "adminnetworkpolicies", true},
		{"baselineadminnetworkpolicies resource", "baselineadminnetworkpolicies", true},
		{"NetworkPolicy is not ANP", "NetworkPolicy", false},
		{"CiliumNetworkPolicy is not ANP", "CiliumNetworkPolicy", false},
		{"pods is not ANP", "pods", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsAdminNetworkPolicyResource(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertUnstructuredToAdminNetworkPolicyResource_Nil(t *testing.T) {
	result, err := ConvertUnstructuredToAdminNetworkPolicyResource(nil)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "cannot convert nil object")
}

func TestConvertUnstructuredToAdminNetworkPolicyResource_AdminNetworkPolicy(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "policy.networking.k8s.io/v1alpha1",
			"kind":       "AdminNetworkPolicy",
			"metadata": map[string]any{
				"name":            "test-anp",
				"uid":             "test-uid",
				"resourceVersion": "123",
				"labels":          map[string]any{"app": "test"},
			},
			"spec": map[string]any{
				"priority": int64(100),
				"subject": map[string]any{
					"namespaces": map[string]any{
						"matchLabels": map[string]any{"env": "prod"},
					},
				},
				"ingress": []any{
					map[string]any{
						"name":   "allow-monitoring",
						"action": "Allow",
						"from": []any{
							map[string]any{
								"namespaces": map[string]any{
									"matchLabels": map[string]any{"role": "monitoring"},
								},
							},
						},
						"ports": []any{
							map[string]any{
								"portNumber": map[string]any{
									"protocol": "TCP",
									"port":     int64(9090),
								},
							},
						},
					},
				},
				"egress": []any{
					map[string]any{
						"name":   "deny-external",
						"action": "Deny",
						"to": []any{
							map[string]any{
								"networks": []any{"0.0.0.0/0"},
							},
						},
					},
				},
			},
		},
	}

	result, err := ConvertUnstructuredToAdminNetworkPolicyResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "AdminNetworkPolicy", result.GetKind())
	assert.Equal(t, "policy.networking.k8s.io", result.GetApiGroup())
	assert.Equal(t, "v1alpha1", result.GetApiVersion())
	assert.Equal(t, "test-anp", result.GetName())
	assert.Equal(t, "test-uid", result.GetUid())

	anpData := result.GetAdminNetworkPolicy()
	require.NotNil(t, anpData)
	assert.Equal(t, int32(100), anpData.GetPriority())

	// Subject
	require.NotNil(t, anpData.GetSubject())
	require.NotNil(t, anpData.GetSubject().GetNamespaces())
	assert.Equal(t, "prod", anpData.GetSubject().GetNamespaces().GetMatchLabels()["env"])

	// Ingress
	require.Len(t, anpData.GetIngress(), 1)
	assert.Equal(t, "allow-monitoring", anpData.GetIngress()[0].GetName())
	assert.Equal(t, "Allow", anpData.GetIngress()[0].GetAction())
	require.Len(t, anpData.GetIngress()[0].GetPeers(), 1)
	require.NotNil(t, anpData.GetIngress()[0].GetPeers()[0].GetNamespaces())
	assert.Equal(t, "monitoring", anpData.GetIngress()[0].GetPeers()[0].GetNamespaces().GetMatchLabels()["role"])
	require.Len(t, anpData.GetIngress()[0].GetPorts(), 1)
	require.NotNil(t, anpData.GetIngress()[0].GetPorts()[0].GetPortNumber())
	assert.Equal(t, "TCP", anpData.GetIngress()[0].GetPorts()[0].GetPortNumber().GetProtocol())
	assert.Equal(t, int32(9090), anpData.GetIngress()[0].GetPorts()[0].GetPortNumber().GetPort())

	// Egress
	require.Len(t, anpData.GetEgress(), 1)
	assert.Equal(t, "deny-external", anpData.GetEgress()[0].GetName())
	assert.Equal(t, "Deny", anpData.GetEgress()[0].GetAction())
	require.Len(t, anpData.GetEgress()[0].GetPeers(), 1)
	assert.Equal(t, []string{"0.0.0.0/0"}, anpData.GetEgress()[0].GetPeers()[0].GetNetworks())
}

func TestConvertUnstructuredToAdminNetworkPolicyResource_BaselineAdminNetworkPolicy(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "policy.networking.k8s.io/v1alpha1",
			"kind":       "BaselineAdminNetworkPolicy",
			"metadata": map[string]any{
				"name":            "default",
				"uid":             "banp-uid",
				"resourceVersion": "456",
			},
			"spec": map[string]any{
				"subject": map[string]any{
					"pods": map[string]any{
						"namespaceSelector": map[string]any{
							"matchLabels": map[string]any{"kubernetes.io/metadata.name": "my-namespace"},
						},
						"podSelector": map[string]any{
							"matchLabels": map[string]any{"app": "web"},
						},
					},
				},
				"ingress": []any{
					map[string]any{
						"name":   "default-deny",
						"action": "Deny",
						"from": []any{
							map[string]any{
								"namespaces": map[string]any{},
							},
						},
					},
				},
			},
		},
	}

	result, err := ConvertUnstructuredToAdminNetworkPolicyResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "BaselineAdminNetworkPolicy", result.GetKind())
	assert.Equal(t, "policy.networking.k8s.io", result.GetApiGroup())

	banpData := result.GetBaselineAdminNetworkPolicy()
	require.NotNil(t, banpData)

	// Subject with pods
	require.NotNil(t, banpData.GetSubject())
	require.NotNil(t, banpData.GetSubject().GetPods())
	assert.Equal(t, "my-namespace", banpData.GetSubject().GetPods().GetNamespaceSelector().GetMatchLabels()["kubernetes.io/metadata.name"])
	assert.Equal(t, "web", banpData.GetSubject().GetPods().GetPodSelector().GetMatchLabels()["app"])

	// Ingress
	require.Len(t, banpData.GetIngress(), 1)
	assert.Equal(t, "default-deny", banpData.GetIngress()[0].GetName())
	assert.Equal(t, "Deny", banpData.GetIngress()[0].GetAction())
}

func TestConvertUnstructuredToAdminNetworkPolicyResource_UnsupportedKind(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "policy.networking.k8s.io/v1alpha1",
			"kind":       "UnknownPolicy",
			"metadata": map[string]any{
				"name": "test",
			},
		},
	}

	result, err := ConvertUnstructuredToAdminNetworkPolicyResource(obj)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "unsupported AdminNetworkPolicy resource kind")
}

func TestConvertUnstructuredToAdminNetworkPolicyResource_WithPortRange(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "policy.networking.k8s.io/v1alpha1",
			"kind":       "AdminNetworkPolicy",
			"metadata": map[string]any{
				"name":            "port-range-anp",
				"uid":             "pr-uid",
				"resourceVersion": "789",
			},
			"spec": map[string]any{
				"priority": int64(50),
				"subject": map[string]any{
					"namespaces": map[string]any{},
				},
				"ingress": []any{
					map[string]any{
						"name":   "allow-range",
						"action": "Allow",
						"from": []any{
							map[string]any{
								"namespaces": map[string]any{},
							},
						},
						"ports": []any{
							map[string]any{
								"portRange": map[string]any{
									"protocol": "TCP",
									"start":    int64(8000),
									"end":      int64(9000),
								},
							},
							map[string]any{
								"namedPort": "http",
							},
						},
					},
				},
			},
		},
	}

	result, err := ConvertUnstructuredToAdminNetworkPolicyResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	anpData := result.GetAdminNetworkPolicy()
	require.NotNil(t, anpData)
	require.Len(t, anpData.GetIngress(), 1)
	require.Len(t, anpData.GetIngress()[0].GetPorts(), 2)

	// Port range
	require.NotNil(t, anpData.GetIngress()[0].GetPorts()[0].GetPortRange())
	assert.Equal(t, "TCP", anpData.GetIngress()[0].GetPorts()[0].GetPortRange().GetProtocol())
	assert.Equal(t, int32(8000), anpData.GetIngress()[0].GetPorts()[0].GetPortRange().GetStart())
	assert.Equal(t, int32(9000), anpData.GetIngress()[0].GetPorts()[0].GetPortRange().GetEnd())

	// Named port
	require.NotNil(t, anpData.GetIngress()[0].GetPorts()[1].GetNamedPort())
	assert.Equal(t, "http", anpData.GetIngress()[0].GetPorts()[1].GetNamedPort())
}

func TestConvertUnstructuredToAdminNetworkPolicyResource_EgressWithNodesAndDomains(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "policy.networking.k8s.io/v1alpha1",
			"kind":       "AdminNetworkPolicy",
			"metadata": map[string]any{
				"name":            "egress-anp",
				"uid":             "eg-uid",
				"resourceVersion": "101",
			},
			"spec": map[string]any{
				"priority": int64(200),
				"subject": map[string]any{
					"namespaces": map[string]any{
						"matchLabels": map[string]any{"env": "staging"},
					},
				},
				"egress": []any{
					map[string]any{
						"name":   "allow-dns",
						"action": "Allow",
						"to": []any{
							map[string]any{
								"domainNames": []any{"*.kubernetes.io", "api.example.com"},
							},
							map[string]any{
								"nodes": map[string]any{
									"matchLabels": map[string]any{"node-role.kubernetes.io/control-plane": ""},
								},
							},
						},
					},
				},
			},
		},
	}

	result, err := ConvertUnstructuredToAdminNetworkPolicyResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	anpData := result.GetAdminNetworkPolicy()
	require.NotNil(t, anpData)
	require.Len(t, anpData.GetEgress(), 1)
	require.Len(t, anpData.GetEgress()[0].GetPeers(), 2)

	// Domain names peer
	assert.Equal(t, []string{"*.kubernetes.io", "api.example.com"}, anpData.GetEgress()[0].GetPeers()[0].GetDomainNames())

	// Nodes peer
	require.NotNil(t, anpData.GetEgress()[0].GetPeers()[1].GetNodes())
	assert.Empty(t, anpData.GetEgress()[0].GetPeers()[1].GetNodes().GetMatchLabels()["node-role.kubernetes.io/control-plane"])
}
