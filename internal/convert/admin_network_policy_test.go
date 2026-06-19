// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

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

	assert.Equal(t, "AdminNetworkPolicy", result.Kind)
	assert.Equal(t, "policy.networking.k8s.io", result.ApiGroup)
	assert.Equal(t, "v1alpha1", result.ApiVersion)
	assert.Equal(t, "test-anp", result.Name)
	assert.Equal(t, "test-uid", result.Uid)

	anpData := result.GetAdminNetworkPolicy()
	require.NotNil(t, anpData)
	assert.Equal(t, int32(100), anpData.Priority)

	// Subject
	require.NotNil(t, anpData.Subject)
	require.NotNil(t, anpData.Subject.Namespaces)
	assert.Equal(t, "prod", anpData.Subject.Namespaces.MatchLabels["env"])

	// Ingress
	require.Len(t, anpData.Ingress, 1)
	assert.Equal(t, "allow-monitoring", *anpData.Ingress[0].Name)
	assert.Equal(t, "Allow", anpData.Ingress[0].Action)
	require.Len(t, anpData.Ingress[0].Peers, 1)
	require.NotNil(t, anpData.Ingress[0].Peers[0].Namespaces)
	assert.Equal(t, "monitoring", anpData.Ingress[0].Peers[0].Namespaces.MatchLabels["role"])
	require.Len(t, anpData.Ingress[0].Ports, 1)
	require.NotNil(t, anpData.Ingress[0].Ports[0].PortNumber)
	assert.Equal(t, "TCP", anpData.Ingress[0].Ports[0].PortNumber.Protocol)
	assert.Equal(t, int32(9090), anpData.Ingress[0].Ports[0].PortNumber.Port)

	// Egress
	require.Len(t, anpData.Egress, 1)
	assert.Equal(t, "deny-external", *anpData.Egress[0].Name)
	assert.Equal(t, "Deny", anpData.Egress[0].Action)
	require.Len(t, anpData.Egress[0].Peers, 1)
	assert.Equal(t, []string{"0.0.0.0/0"}, anpData.Egress[0].Peers[0].Networks)
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

	assert.Equal(t, "BaselineAdminNetworkPolicy", result.Kind)
	assert.Equal(t, "policy.networking.k8s.io", result.ApiGroup)

	banpData := result.GetBaselineAdminNetworkPolicy()
	require.NotNil(t, banpData)

	// Subject with pods
	require.NotNil(t, banpData.Subject)
	require.NotNil(t, banpData.Subject.Pods)
	assert.Equal(t, "my-namespace", banpData.Subject.Pods.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"])
	assert.Equal(t, "web", banpData.Subject.Pods.PodSelector.MatchLabels["app"])

	// Ingress
	require.Len(t, banpData.Ingress, 1)
	assert.Equal(t, "default-deny", *banpData.Ingress[0].Name)
	assert.Equal(t, "Deny", banpData.Ingress[0].Action)
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
	require.Len(t, anpData.Ingress, 1)
	require.Len(t, anpData.Ingress[0].Ports, 2)

	// Port range
	require.NotNil(t, anpData.Ingress[0].Ports[0].PortRange)
	assert.Equal(t, "TCP", anpData.Ingress[0].Ports[0].PortRange.Protocol)
	assert.Equal(t, int32(8000), anpData.Ingress[0].Ports[0].PortRange.Start)
	assert.Equal(t, int32(9000), anpData.Ingress[0].Ports[0].PortRange.End)

	// Named port
	require.NotNil(t, anpData.Ingress[0].Ports[1].NamedPort)
	assert.Equal(t, "http", *anpData.Ingress[0].Ports[1].NamedPort)
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
	require.Len(t, anpData.Egress, 1)
	require.Len(t, anpData.Egress[0].Peers, 2)

	// Domain names peer
	assert.Equal(t, []string{"*.kubernetes.io", "api.example.com"}, anpData.Egress[0].Peers[0].DomainNames)

	// Nodes peer
	require.NotNil(t, anpData.Egress[0].Peers[1].Nodes)
	assert.Equal(t, "", anpData.Egress[0].Peers[1].Nodes.MatchLabels["node-role.kubernetes.io/control-plane"])
}
