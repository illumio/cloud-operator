// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestIsCiliumPolicy(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"CiliumNetworkPolicy kind", "CiliumNetworkPolicy", true},
		{"CiliumClusterwideNetworkPolicy kind", "CiliumClusterwideNetworkPolicy", true},
		{"ciliumnetworkpolicies resource", "ciliumnetworkpolicies", true},
		{"ciliumclusterwidenetworkpolicies resource", "ciliumclusterwidenetworkpolicies", true},
		{"NetworkPolicy is not Cilium", "NetworkPolicy", false},
		{"pods is not Cilium", "pods", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsCiliumPolicy(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertUnstructuredToCiliumPolicy_Nil(t *testing.T) {
	result, err := ConvertUnstructuredToCiliumPolicy(nil)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestConvertUnstructuredToCiliumPolicy_Basic(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "test-policy",
				"namespace":       "default",
				"uid":             "test-uid",
				"resourceVersion": "12345",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{
					"matchLabels": map[string]any{
						"app": "test",
					},
				},
				"ingress": []any{
					map[string]any{
						"fromEndpoints": []any{
							map[string]any{
								"matchLabels": map[string]any{
									"role": "frontend",
								},
							},
						},
					},
				},
			},
		},
	}

	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2",
		Kind:    "CiliumNetworkPolicy",
	})

	result, err := ConvertUnstructuredToCiliumPolicy(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "test-policy", result.Name)
	assert.Equal(t, "default", result.GetNamespace())
	assert.Equal(t, "CiliumNetworkPolicy", result.Kind)
	assert.Equal(t, "test-uid", result.Uid)
	assert.Equal(t, "12345", result.ResourceVersion)

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.Specs, 1)

	spec := ciliumPolicy.Specs[0]
	require.NotNil(t, spec.EndpointSelector)
	assert.Equal(t, "test", spec.EndpointSelector.MatchLabels["app"])

	require.Len(t, spec.IngressRules, 1)
	ingressRule := spec.IngressRules[0]
	require.NotNil(t, ingressRule.FromEndpoints)
	require.Len(t, ingressRule.FromEndpoints.GetItems(), 1)
	assert.Equal(t, "frontend", ingressRule.FromEndpoints.GetItems()[0].MatchLabels["role"])
}

func TestConvertUnstructuredToCiliumPolicy_ClusterwidePolicy(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumClusterwideNetworkPolicy",
			"metadata": map[string]any{
				"name":            "clusterwide-policy",
				"uid":             "clusterwide-uid",
				"resourceVersion": "67890",
			},
			"spec": map[string]any{
				"nodeSelector": map[string]any{
					"matchLabels": map[string]any{
						"node-type": "worker",
					},
				},
			},
		},
	}

	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2",
		Kind:    "CiliumClusterwideNetworkPolicy",
	})

	result, err := ConvertUnstructuredToCiliumPolicy(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "clusterwide-policy", result.Name)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", result.Kind)

	ciliumPolicy := result.GetCiliumClusterwideNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.Specs, 1)

	spec := ciliumPolicy.Specs[0]
	require.NotNil(t, spec.NodeSelector)
	assert.Equal(t, "worker", spec.NodeSelector.MatchLabels["node-type"])
}

func TestConvertUnstructuredToCiliumPolicy_WithPorts(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "port-policy",
				"namespace":       "default",
				"uid":             "port-uid",
				"resourceVersion": "11111",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"ingress": []any{
					map[string]any{
						"toPorts": []any{
							map[string]any{
								"ports": []any{
									map[string]any{
										"port":     "80",
										"protocol": "TCP",
									},
									map[string]any{
										"port":     "443",
										"protocol": "TCP",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2",
		Kind:    "CiliumNetworkPolicy",
	})

	result, err := ConvertUnstructuredToCiliumPolicy(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.Specs, 1)

	spec := ciliumPolicy.Specs[0]
	require.Len(t, spec.IngressRules, 1)

	ingressRule := spec.IngressRules[0]
	require.Len(t, ingressRule.ToPorts, 1)

	portRule := ingressRule.ToPorts[0]
	require.Len(t, portRule.Ports, 2)
	assert.Equal(t, "80", portRule.Ports[0].Port)
	assert.Equal(t, "TCP", portRule.Ports[0].GetProtocol())
	assert.Equal(t, "443", portRule.Ports[1].Port)
}

func TestConvertUnstructuredToCiliumPolicy_WithCIDR(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "cidr-policy",
				"namespace":       "default",
				"uid":             "cidr-uid",
				"resourceVersion": "22222",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"egress": []any{
					map[string]any{
						"toCIDR": []any{"10.0.0.0/8", "192.168.0.0/16"},
						"toCIDRSet": []any{
							map[string]any{
								"cidr":   "172.16.0.0/12",
								"except": []any{"172.16.1.0/24"},
							},
						},
					},
				},
			},
		},
	}

	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2",
		Kind:    "CiliumNetworkPolicy",
	})

	result, err := ConvertUnstructuredToCiliumPolicy(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.Specs, 1)

	spec := ciliumPolicy.Specs[0]
	require.Len(t, spec.EgressRules, 1)

	egressRule := spec.EgressRules[0]
	assert.ElementsMatch(t, []string{"10.0.0.0/8", "192.168.0.0/16"}, egressRule.ToCidr)

	require.Len(t, egressRule.ToCidrSet, 1)
	assert.Equal(t, "172.16.0.0/12", egressRule.ToCidrSet[0].GetCidr())
	assert.ElementsMatch(t, []string{"172.16.1.0/24"}, egressRule.ToCidrSet[0].Except)
}

func TestConvertUnstructuredToCiliumPolicy_MultipleSpecs(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "multi-spec-policy",
				"namespace":       "default",
				"uid":             "multi-uid",
				"resourceVersion": "33333",
			},
			"specs": []any{
				map[string]any{
					"endpointSelector": map[string]any{
						"matchLabels": map[string]any{"app": "web"},
					},
					"description": "Allow web traffic",
				},
				map[string]any{
					"endpointSelector": map[string]any{
						"matchLabels": map[string]any{"app": "api"},
					},
					"description": "Allow API traffic",
				},
			},
		},
	}

	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2",
		Kind:    "CiliumNetworkPolicy",
	})

	result, err := ConvertUnstructuredToCiliumPolicy(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.Specs, 2)

	assert.Equal(t, "web", ciliumPolicy.Specs[0].EndpointSelector.MatchLabels["app"])
	assert.Equal(t, "Allow web traffic", ciliumPolicy.Specs[0].GetDescription())

	assert.Equal(t, "api", ciliumPolicy.Specs[1].EndpointSelector.MatchLabels["app"])
	assert.Equal(t, "Allow API traffic", ciliumPolicy.Specs[1].GetDescription())
}
