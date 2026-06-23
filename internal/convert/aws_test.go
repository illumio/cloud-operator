// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestIsAWSResource(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"ClusterNetworkPolicy kind", "ClusterNetworkPolicy", true},
		{"clusternetworkpolicies resource", "clusternetworkpolicies", true},
		{"CiliumNetworkPolicy is not AWS", "CiliumNetworkPolicy", false},
		{"NetworkPolicy is not AWS", "NetworkPolicy", false},
		{"pods is not AWS", "pods", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsAWSResource(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertUnstructuredToAWSResource_Nil(t *testing.T) {
	result, err := ConvertUnstructuredToAWSResource(nil)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "cannot convert nil object")
}

func TestConvertUnstructuredToAWSResource_UnsupportedKind(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]any{}}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "networking.k8s.aws",
		Version: "v1alpha1",
		Kind:    "SecurityGroupPolicy",
	})

	result, err := ConvertUnstructuredToAWSResource(obj)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "unsupported AWS resource kind")
}

// newCNP builds an unstructured ClusterNetworkPolicy with the given spec.
func newCNP(name string, spec map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "networking.k8s.aws/v1alpha1",
			"kind":       "ClusterNetworkPolicy",
			"metadata": map[string]any{
				"name":            name,
				"uid":             "test-uid",
				"resourceVersion": "12345",
			},
			"spec": spec,
		},
	}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "networking.k8s.aws",
		Version: "v1alpha1",
		Kind:    "ClusterNetworkPolicy",
	})

	return obj
}

func TestConvertUnstructuredToAWSResource_Metadata(t *testing.T) {
	obj := newCNP("test-policy", map[string]any{
		"priority": int64(100),
		"tier":     "Admin",
		"subject": map[string]any{
			"namespaces": map[string]any{
				"matchLabels": map[string]any{"environment": "production"},
			},
		},
	})

	result, err := ConvertUnstructuredToAWSResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "test-policy", result.GetName())
	assert.Empty(t, result.GetNamespace()) // cluster-scoped
	assert.Equal(t, "ClusterNetworkPolicy", result.GetKind())
	assert.Equal(t, "test-uid", result.GetUid())
	assert.Equal(t, "12345", result.GetResourceVersion())
	assert.Equal(t, "networking.k8s.aws", result.GetApiGroup())
	assert.Equal(t, "v1alpha1", result.GetApiVersion())

	cnp := result.GetAwsClusterNetworkPolicy()
	require.NotNil(t, cnp)
	assert.Equal(t, int32(100), cnp.GetPriority())
	assert.Equal(t, "Admin", cnp.GetTier())
	require.NotNil(t, cnp.GetSubject())
	require.NotNil(t, cnp.GetSubject().GetNamespaces())
	assert.Equal(t, "production", cnp.GetSubject().GetNamespaces().GetMatchLabels()["environment"])
}

func TestConvertUnstructuredToAWSResource_IngressWithPodSubjectAndPorts(t *testing.T) {
	obj := newCNP("allow-web", map[string]any{
		"priority": int64(100),
		"tier":     "Baseline",
		"subject": map[string]any{
			"pods": map[string]any{
				"namespaceSelector": map[string]any{
					"matchLabels": map[string]any{"kubernetes.io/metadata.name": "test-namespace"},
				},
				"podSelector": map[string]any{
					"matchLabels": map[string]any{"app": "web"},
				},
			},
		},
		"ingress": []any{
			map[string]any{
				"action": "Accept",
				"ports": []any{
					map[string]any{"portNumber": map[string]any{"port": int64(80), "protocol": "TCP"}},
					map[string]any{"portNumber": map[string]any{"port": int64(443), "protocol": "TCP"}},
				},
				"from": []any{
					map[string]any{
						"pods": map[string]any{
							"namespaceSelector": map[string]any{
								"matchLabels": map[string]any{"kubernetes.io/metadata.name": "test-namespace"},
							},
							"podSelector": map[string]any{
								"matchLabels": map[string]any{"app": "frontend"},
							},
						},
					},
				},
			},
		},
	})

	result, err := ConvertUnstructuredToAWSResource(obj)
	require.NoError(t, err)

	cnp := result.GetAwsClusterNetworkPolicy()
	require.NotNil(t, cnp)

	// Subject pod selectors.
	pods := cnp.GetSubject().GetPods()
	require.NotNil(t, pods)
	assert.Equal(t, "test-namespace", pods.GetNamespaceSelector().GetMatchLabels()["kubernetes.io/metadata.name"])
	assert.Equal(t, "web", pods.GetPodSelector().GetMatchLabels()["app"])

	require.Len(t, cnp.GetIngress(), 1)
	rule := cnp.GetIngress()[0]
	assert.Equal(t, "Accept", rule.GetAction())

	require.Len(t, rule.GetPorts(), 2)
	assert.Equal(t, int32(80), rule.GetPorts()[0].GetPortNumber().GetPort())
	assert.Equal(t, "TCP", rule.GetPorts()[0].GetPortNumber().GetProtocol())
	assert.Equal(t, int32(443), rule.GetPorts()[1].GetPortNumber().GetPort())

	require.Len(t, rule.GetFrom(), 1)
	fromPods := rule.GetFrom()[0].GetPods()
	require.NotNil(t, fromPods)
	assert.Equal(t, "frontend", fromPods.GetPodSelector().GetMatchLabels()["app"])
}

func TestConvertUnstructuredToAWSResource_EgressWithNetworksAndDomainNames(t *testing.T) {
	obj := newCNP("allow-external", map[string]any{
		"priority": int64(200),
		"tier":     "Baseline",
		"subject": map[string]any{
			"pods": map[string]any{
				"namespaceSelector": map[string]any{"matchLabels": map[string]any{}},
				"podSelector":       map[string]any{"matchLabels": map[string]any{"app": "api"}},
			},
		},
		"egress": []any{
			map[string]any{
				"action": "Accept",
				"to": []any{
					map[string]any{
						"networks": []any{"10.0.0.0/8", "192.168.1.0/24"},
					},
				},
			},
			map[string]any{
				"action": "Accept",
				"to": []any{
					map[string]any{
						"domainNames": []any{"api.example.com", "*.amazonaws.com"},
					},
				},
			},
		},
	})

	result, err := ConvertUnstructuredToAWSResource(obj)
	require.NoError(t, err)

	cnp := result.GetAwsClusterNetworkPolicy()
	require.NotNil(t, cnp)
	require.Len(t, cnp.GetEgress(), 2)

	require.Len(t, cnp.GetEgress()[0].GetTo(), 1)
	assert.Equal(t, []string{"10.0.0.0/8", "192.168.1.0/24"}, cnp.GetEgress()[0].GetTo()[0].GetNetworks())

	require.Len(t, cnp.GetEgress()[1].GetTo(), 1)
	assert.Equal(t, []string{"api.example.com", "*.amazonaws.com"}, cnp.GetEgress()[1].GetTo()[0].GetDomainNames())
}

func TestConvertUnstructuredToAWSResource_PortRangeAndNamedPort(t *testing.T) {
	obj := newCNP("ports", map[string]any{
		"priority": int64(300),
		"tier":     "Baseline",
		"subject":  map[string]any{"namespaces": map[string]any{"matchLabels": map[string]any{}}},
		"ingress": []any{
			map[string]any{
				"action": "Accept",
				"ports": []any{
					map[string]any{"portRange": map[string]any{"start": int64(8000), "end": int64(9000), "protocol": "TCP"}},
					map[string]any{"namedPort": "http"},
				},
				"from": []any{},
			},
		},
	})

	result, err := ConvertUnstructuredToAWSResource(obj)
	require.NoError(t, err)

	rule := result.GetAwsClusterNetworkPolicy().GetIngress()[0]
	require.Len(t, rule.GetPorts(), 2)

	pr := rule.GetPorts()[0].GetPortRange()
	require.NotNil(t, pr)
	assert.Equal(t, int32(8000), pr.GetStart())
	assert.Equal(t, int32(9000), pr.GetEnd())
	assert.Equal(t, "TCP", pr.GetProtocol())

	assert.Equal(t, "http", rule.GetPorts()[1].GetNamedPort())
}

func TestConvertUnstructuredToAWSResource_DenyAndPassActions(t *testing.T) {
	obj := newCNP("deny-pass", map[string]any{
		"priority": int64(1000),
		"tier":     "Admin",
		"subject":  map[string]any{"namespaces": map[string]any{"matchLabels": map[string]any{"environment": "production"}}},
		"ingress": []any{
			map[string]any{
				"action": "Deny",
				"from": []any{
					map[string]any{"namespaces": map[string]any{"matchLabels": map[string]any{"environment": "development"}}},
				},
			},
		},
		"egress": []any{
			map[string]any{
				"action": "Pass",
				"to": []any{
					map[string]any{"networks": []any{"0.0.0.0/0"}},
				},
			},
		},
	})

	result, err := ConvertUnstructuredToAWSResource(obj)
	require.NoError(t, err)

	cnp := result.GetAwsClusterNetworkPolicy()
	require.Len(t, cnp.GetIngress(), 1)
	assert.Equal(t, "Deny", cnp.GetIngress()[0].GetAction())
	assert.Equal(t, "development",
		cnp.GetIngress()[0].GetFrom()[0].GetNamespaces().GetMatchLabels()["environment"])

	require.Len(t, cnp.GetEgress(), 1)
	assert.Equal(t, "Pass", cnp.GetEgress()[0].GetAction())
	assert.Equal(t, []string{"0.0.0.0/0"}, cnp.GetEgress()[0].GetTo()[0].GetNetworks())
}

func TestConvertUnstructuredToAWSResource_RuleName(t *testing.T) {
	obj := newCNP("named-rule", map[string]any{
		"priority": int64(100),
		"tier":     "Baseline",
		"subject":  map[string]any{"namespaces": map[string]any{"matchLabels": map[string]any{}}},
		"ingress": []any{
			map[string]any{
				"name":   "allow-frontend",
				"action": "Accept",
				"from":   []any{},
			},
		},
	})

	result, err := ConvertUnstructuredToAWSResource(obj)
	require.NoError(t, err)

	rule := result.GetAwsClusterNetworkPolicy().GetIngress()[0]
	assert.Equal(t, "allow-frontend", rule.GetName())
}

func TestConvertUnstructuredToAWSResource_MatchExpressions(t *testing.T) {
	obj := newCNP("expr", map[string]any{
		"priority": int64(100),
		"tier":     "Baseline",
		"subject": map[string]any{
			"namespaces": map[string]any{
				"matchExpressions": []any{
					map[string]any{
						"key":      "tier",
						"operator": "In",
						"values":   []any{"frontend", "backend"},
					},
				},
			},
		},
	})

	result, err := ConvertUnstructuredToAWSResource(obj)
	require.NoError(t, err)

	sel := result.GetAwsClusterNetworkPolicy().GetSubject().GetNamespaces()
	require.NotNil(t, sel)
	require.Len(t, sel.GetMatchExpressions(), 1)
	expr := sel.GetMatchExpressions()[0]
	assert.Equal(t, "tier", expr.GetKey())
	assert.Equal(t, "In", expr.GetOperator())
	assert.Equal(t, []string{"frontend", "backend"}, expr.GetValues())
}
