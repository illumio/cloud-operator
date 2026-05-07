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
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "cannot convert nil object")
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

	assert.Equal(t, "test-policy", result.GetName())
	assert.Equal(t, "default", result.GetNamespace())
	assert.Equal(t, "CiliumNetworkPolicy", result.GetKind())
	assert.Equal(t, "test-uid", result.GetUid())
	assert.Equal(t, "12345", result.GetResourceVersion())
	assert.Equal(t, "cilium.io", result.GetApiGroup())
	assert.Equal(t, "v2", result.GetApiVersion())

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.NotNil(t, spec.GetEndpointSelector())
	assert.Equal(t, "test", spec.GetEndpointSelector().GetMatchLabels()["app"])

	require.Len(t, spec.GetIngressRules(), 1)
	ingressRule := spec.GetIngressRules()[0]
	require.NotNil(t, ingressRule.GetFromEndpoints())
	require.Len(t, ingressRule.GetFromEndpoints().GetItems(), 1)
	assert.Equal(t, "frontend", ingressRule.GetFromEndpoints().GetItems()[0].GetMatchLabels()["role"])
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

	assert.Equal(t, "clusterwide-policy", result.GetName())
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", result.GetKind())
	assert.Empty(t, result.GetNamespace(), "cluster-scoped policy should have no namespace")
	assert.Equal(t, "cilium.io", result.GetApiGroup())
	assert.Equal(t, "v2", result.GetApiVersion())

	ciliumPolicy := result.GetCiliumClusterwideNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.NotNil(t, spec.GetNodeSelector())
	assert.Equal(t, "worker", spec.GetNodeSelector().GetMatchLabels()["node-type"])
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
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetIngressRules(), 1)

	ingressRule := spec.GetIngressRules()[0]
	require.Len(t, ingressRule.GetToPorts(), 1)

	portRule := ingressRule.GetToPorts()[0]
	require.Len(t, portRule.GetPorts(), 2)
	assert.Equal(t, "80", portRule.GetPorts()[0].GetPort())
	assert.Equal(t, "TCP", portRule.GetPorts()[0].GetProtocol())
	assert.Equal(t, "443", portRule.GetPorts()[1].GetPort())
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
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetEgressRules(), 1)

	egressRule := spec.GetEgressRules()[0]
	assert.ElementsMatch(t, []string{"10.0.0.0/8", "192.168.0.0/16"}, egressRule.GetToCidr())

	require.Len(t, egressRule.GetToCidrSet(), 1)
	assert.Equal(t, "172.16.0.0/12", egressRule.GetToCidrSet()[0].GetCidr())
	assert.ElementsMatch(t, []string{"172.16.1.0/24"}, egressRule.GetToCidrSet()[0].GetExcept())
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
	require.Len(t, ciliumPolicy.GetSpecs(), 2)

	assert.Equal(t, "web", ciliumPolicy.GetSpecs()[0].GetEndpointSelector().GetMatchLabels()["app"])
	assert.Equal(t, "Allow web traffic", ciliumPolicy.GetSpecs()[0].GetDescription())

	assert.Equal(t, "api", ciliumPolicy.GetSpecs()[1].GetEndpointSelector().GetMatchLabels()["app"])
	assert.Equal(t, "Allow API traffic", ciliumPolicy.GetSpecs()[1].GetDescription())
}

func TestConvertUnstructuredToCiliumPolicy_WithDefaultDeny(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "default-deny-policy",
				"namespace":       "default",
				"uid":             "dd-uid",
				"resourceVersion": "44444",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"enableDefaultDeny": map[string]any{
					"ingress": true,
					"egress":  false,
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
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.NotNil(t, spec.GetEnableDefaultDeny())
	assert.True(t, spec.GetEnableDefaultDeny().GetIngress())
	assert.False(t, spec.GetEnableDefaultDeny().GetEgress())
}

func TestConvertUnstructuredToCiliumPolicy_WithAWSGroups(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "aws-groups-policy",
				"namespace":       "default",
				"uid":             "aws-uid",
				"resourceVersion": "55555",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"ingress": []any{
					map[string]any{
						"fromGroups": []any{
							map[string]any{
								"aws": map[string]any{
									"labels": map[string]any{
										"env": "production",
									},
									"securityGroupsIds":   []any{"sg-12345", "sg-67890"},
									"securityGroupsNames": []any{"web-sg", "api-sg"},
									"region":              "us-west-2",
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
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetIngressRules(), 1)

	ingressRule := spec.GetIngressRules()[0]
	require.Len(t, ingressRule.GetFromGroups(), 1)

	group := ingressRule.GetFromGroups()[0]
	awsGroup := group.GetAws()
	require.NotNil(t, awsGroup)
	assert.Equal(t, "production", awsGroup.GetLabels()["env"])
	assert.ElementsMatch(t, []string{"sg-12345", "sg-67890"}, awsGroup.GetSecurityGroupIds())
	assert.ElementsMatch(t, []string{"web-sg", "api-sg"}, awsGroup.GetSecurityGroupNames())
	assert.Equal(t, "us-west-2", awsGroup.GetRegion())
}

func TestConvertUnstructuredToCiliumPolicy_WithNodeSelectors(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "node-selector-policy",
				"namespace":       "default",
				"uid":             "ns-uid",
				"resourceVersion": "66666",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"ingress": []any{
					map[string]any{
						"fromNodes": []any{
							map[string]any{
								"matchLabels": map[string]any{
									"node-type": "worker",
								},
							},
						},
					},
				},
				"egress": []any{
					map[string]any{
						"toNodes": []any{
							map[string]any{
								"matchLabels": map[string]any{
									"node-type": "control-plane",
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
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]

	require.Len(t, spec.GetIngressRules(), 1)
	require.Len(t, spec.GetIngressRules()[0].GetFromNodes(), 1)
	assert.Equal(t, "worker", spec.GetIngressRules()[0].GetFromNodes()[0].GetMatchLabels()["node-type"])

	require.Len(t, spec.GetEgressRules(), 1)
	require.Len(t, spec.GetEgressRules()[0].GetToNodes(), 1)
	assert.Equal(t, "control-plane", spec.GetEgressRules()[0].GetToNodes()[0].GetMatchLabels()["node-type"])
}

func TestConvertUnstructuredToCiliumPolicy_WithICMP(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "icmp-policy",
				"namespace":       "default",
				"uid":             "icmp-uid",
				"resourceVersion": "77777",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"ingress": []any{
					map[string]any{
						"icmps": []any{
							map[string]any{
								"fields": []any{
									map[string]any{
										"family": "IPv4",
										"type":   int64(8),
									},
									map[string]any{
										"family": "IPv6",
										"type":   float64(128),
									},
									map[string]any{
										"family": "IPv4",
										"type":   "EchoReply",
									},
									map[string]any{
										"family": "IPv4",
										"type":   "3",
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
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetIngressRules(), 1)

	ingressRule := spec.GetIngressRules()[0]
	require.Len(t, ingressRule.GetIcmps(), 1)

	icmpRule := ingressRule.GetIcmps()[0]
	require.Len(t, icmpRule.GetFields(), 4)

	assert.Equal(t, "IPv4", icmpRule.GetFields()[0].GetFamily())
	assert.Equal(t, uint32(8), icmpRule.GetFields()[0].GetTypeInt())

	assert.Equal(t, "IPv6", icmpRule.GetFields()[1].GetFamily())
	assert.Equal(t, uint32(128), icmpRule.GetFields()[1].GetTypeInt())

	assert.Equal(t, "IPv4", icmpRule.GetFields()[2].GetFamily())
	assert.Equal(t, "EchoReply", icmpRule.GetFields()[2].GetTypeString())

	assert.Equal(t, "IPv4", icmpRule.GetFields()[3].GetFamily())
	assert.Equal(t, uint32(3), icmpRule.GetFields()[3].GetTypeInt(), "numeric string should parse to TypeInt")
}

func TestConvertUnstructuredToCiliumPolicy_WithAuthentication(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "auth-policy",
				"namespace":       "default",
				"uid":             "auth-uid",
				"resourceVersion": "88888",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"ingress": []any{
					map[string]any{
						"authentication": map[string]any{
							"mode": "required",
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
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetIngressRules(), 1)

	ingressRule := spec.GetIngressRules()[0]
	require.NotNil(t, ingressRule.GetAuthentication())
	assert.Equal(t, "required", ingressRule.GetAuthentication().GetMode())
}

func TestConvertUnstructuredToCiliumPolicy_WithFQDN(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "fqdn-policy",
				"namespace":       "default",
				"uid":             "fqdn-uid",
				"resourceVersion": "99999",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"egress": []any{
					map[string]any{
						"toFQDNs": []any{
							map[string]any{
								"matchName": "api.example.com",
							},
							map[string]any{
								"matchPattern": "*.internal.example.com",
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
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetEgressRules(), 1)

	egressRule := spec.GetEgressRules()[0]
	require.Len(t, egressRule.GetToFqdns(), 2)

	assert.Equal(t, "api.example.com", egressRule.GetToFqdns()[0].GetMatchName())
	assert.Equal(t, "*.internal.example.com", egressRule.GetToFqdns()[1].GetMatchPattern())
}

func TestConvertUnstructuredToCiliumPolicy_WithK8sServices(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "k8s-service-policy",
				"namespace":       "default",
				"uid":             "svc-uid",
				"resourceVersion": "10101",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"egress": []any{
					map[string]any{
						"toServices": []any{
							map[string]any{
								"k8sService": map[string]any{
									"serviceName": "my-service",
									"namespace":   "kube-system",
								},
							},
							map[string]any{
								"k8sServiceSelector": map[string]any{
									"selector": map[string]any{
										"matchLabels": map[string]any{
											"app": "database",
										},
									},
									"namespace": "data",
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
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetEgressRules(), 1)

	egressRule := spec.GetEgressRules()[0]
	require.Len(t, egressRule.GetToServices(), 2)

	k8sService := egressRule.GetToServices()[0].GetK8SService()
	require.NotNil(t, k8sService)
	assert.Equal(t, "my-service", k8sService.GetServiceName())
	assert.Equal(t, "kube-system", k8sService.GetNamespace())

	k8sServiceSelector := egressRule.GetToServices()[1].GetK8SServiceSelector()
	require.NotNil(t, k8sServiceSelector)
	assert.Equal(t, "data", k8sServiceSelector.GetNamespace())
	require.NotNil(t, k8sServiceSelector.GetSelector())
	assert.Equal(t, "database", k8sServiceSelector.GetSelector().GetMatchLabels()["app"])
}

func TestConvertUnstructuredToCiliumPolicy_WithCIDRGroupRef(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "cidr-group-policy",
				"namespace":       "default",
				"uid":             "cidrgrp-uid",
				"resourceVersion": "20202",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"egress": []any{
					map[string]any{
						"toCIDRSet": []any{
							map[string]any{
								"cidrGroupRef": "office-networks",
							},
							map[string]any{
								"cidrGroupSelector": map[string]any{
									"matchLabels": map[string]any{
										"network-type": "trusted",
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
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetEgressRules(), 1)

	egressRule := spec.GetEgressRules()[0]
	require.Len(t, egressRule.GetToCidrSet(), 2)

	assert.Equal(t, "office-networks", egressRule.GetToCidrSet()[0].GetCidrGroupRef())

	cidrGroupSelector := egressRule.GetToCidrSet()[1].GetCidrGroupSelector()
	require.NotNil(t, cidrGroupSelector)
	assert.Equal(t, "trusted", cidrGroupSelector.GetMatchLabels()["network-type"])
}

func TestConvertUnstructuredToCiliumPolicy_WithEndPort(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "port-range-policy",
				"namespace":       "default",
				"uid":             "endport-uid",
				"resourceVersion": "30303",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"ingress": []any{
					map[string]any{
						"toPorts": []any{
							map[string]any{
								"ports": []any{
									map[string]any{
										"port":     "8000",
										"endPort":  int64(8100),
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
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetIngressRules(), 1)

	ingressRule := spec.GetIngressRules()[0]
	require.Len(t, ingressRule.GetToPorts(), 1)

	portRule := ingressRule.GetToPorts()[0]
	require.Len(t, portRule.GetPorts(), 1)

	port := portRule.GetPorts()[0]
	assert.Equal(t, "8000", port.GetPort())
	assert.Equal(t, int32(8100), port.GetEndPort())
	assert.Equal(t, "TCP", port.GetProtocol())
}

func TestConvertUnstructuredToCiliumPolicy_WithDenyRules(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "deny-rules-policy",
				"namespace":       "default",
				"uid":             "deny-uid",
				"resourceVersion": "40404",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"ingressDeny": []any{
					map[string]any{
						"fromEndpoints": []any{
							map[string]any{
								"matchLabels": map[string]any{
									"app": "untrusted",
								},
							},
						},
					},
				},
				"egressDeny": []any{
					map[string]any{
						"toEntities": []any{"world"},
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
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]

	require.Len(t, spec.GetIngressDenyRules(), 1)
	require.NotNil(t, spec.GetIngressDenyRules()[0].GetFromEndpoints())
	assert.Equal(t, "untrusted", spec.GetIngressDenyRules()[0].GetFromEndpoints().GetItems()[0].GetMatchLabels()["app"])

	require.Len(t, spec.GetEgressDenyRules(), 1)
	assert.ElementsMatch(t, []string{"world"}, spec.GetEgressDenyRules()[0].GetToEntities())
}

func TestConvertUnstructuredToCiliumPolicy_WithLabels(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "labeled-policy",
				"namespace":       "default",
				"uid":             "label-uid",
				"resourceVersion": "50505",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"labels": []any{
					map[string]any{"key": "policy-type", "value": "compliance"},
					map[string]any{"key": "owner", "value": "security-team"},
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
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	assert.Equal(t, "compliance", spec.GetLabels()["policy-type"])
	assert.Equal(t, "security-team", spec.GetLabels()["owner"])
}

func TestConvertUnstructuredToCiliumPolicy_WithMatchExpressions(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "expr-policy",
				"namespace":       "default",
				"uid":             "expr-uid",
				"resourceVersion": "60606",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{
					"matchExpressions": []any{
						map[string]any{
							"key":      "env",
							"operator": "In",
							"values":   []any{"prod", "staging"},
						},
						map[string]any{
							"key":      "deprecated",
							"operator": "DoesNotExist",
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
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.NotNil(t, spec.GetEndpointSelector())
	require.Len(t, spec.GetEndpointSelector().GetMatchExpressions(), 2)

	expr1 := spec.GetEndpointSelector().GetMatchExpressions()[0]
	assert.Equal(t, "env", expr1.GetKey())
	assert.Equal(t, "In", expr1.GetOperator())
	assert.ElementsMatch(t, []string{"prod", "staging"}, expr1.GetValues())

	expr2 := spec.GetEndpointSelector().GetMatchExpressions()[1]
	assert.Equal(t, "deprecated", expr2.GetKey())
	assert.Equal(t, "DoesNotExist", expr2.GetOperator())
}

func TestConvertUnstructuredToCiliumPolicy_ComplexEgress(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "complex-egress-policy",
				"namespace":       "default",
				"uid":             "complex-uid",
				"resourceVersion": "70707",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{
					"matchLabels": map[string]any{"app": "web"},
				},
				"egress": []any{
					map[string]any{
						"toEndpoints": []any{
							map[string]any{
								"matchLabels": map[string]any{"app": "database"},
							},
						},
						"toCIDR":     []any{"10.0.0.0/8"},
						"toEntities": []any{"host", "cluster"},
						"toPorts": []any{
							map[string]any{
								"ports": []any{
									map[string]any{
										"port":     "5432",
										"protocol": "TCP",
									},
								},
							},
						},
						"toFQDNs": []any{
							map[string]any{
								"matchName": "db.example.com",
							},
						},
						"toServices": []any{
							map[string]any{
								"k8sService": map[string]any{
									"serviceName": "postgres",
									"namespace":   "data",
								},
							},
						},
						"authentication": map[string]any{
							"mode": "test-only",
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
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetEgressRules(), 1)

	egressRule := spec.GetEgressRules()[0]

	require.NotNil(t, egressRule.GetToEndpoints())
	assert.Equal(t, "database", egressRule.GetToEndpoints().GetItems()[0].GetMatchLabels()["app"])

	assert.ElementsMatch(t, []string{"10.0.0.0/8"}, egressRule.GetToCidr())
	assert.ElementsMatch(t, []string{"host", "cluster"}, egressRule.GetToEntities())

	require.Len(t, egressRule.GetToPorts(), 1)
	assert.Equal(t, "5432", egressRule.GetToPorts()[0].GetPorts()[0].GetPort())
	assert.Equal(t, "TCP", egressRule.GetToPorts()[0].GetPorts()[0].GetProtocol())

	require.Len(t, egressRule.GetToFqdns(), 1)
	assert.Equal(t, "db.example.com", egressRule.GetToFqdns()[0].GetMatchName())

	require.Len(t, egressRule.GetToServices(), 1)
	assert.Equal(t, "postgres", egressRule.GetToServices()[0].GetK8SService().GetServiceName())

	require.NotNil(t, egressRule.GetAuthentication())
	assert.Equal(t, "test-only", egressRule.GetAuthentication().GetMode())
}

func TestConvertUnstructuredToCiliumPolicy_CombinedSpecAndSpecs(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "combined-policy",
				"namespace":       "default",
				"uid":             "combined-uid",
				"resourceVersion": "80808",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{
					"matchLabels": map[string]any{"app": "from-spec"},
				},
				"description": "Single spec rule",
			},
			"specs": []any{
				map[string]any{
					"endpointSelector": map[string]any{
						"matchLabels": map[string]any{"app": "from-specs-1"},
					},
					"description": "First specs rule",
				},
				map[string]any{
					"endpointSelector": map[string]any{
						"matchLabels": map[string]any{"app": "from-specs-2"},
					},
					"description": "Second specs rule",
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
	require.Len(t, ciliumPolicy.GetSpecs(), 3, "should combine spec (1) + specs (2)")

	assert.Equal(t, "from-spec", ciliumPolicy.GetSpecs()[0].GetEndpointSelector().GetMatchLabels()["app"])
	assert.Equal(t, "Single spec rule", ciliumPolicy.GetSpecs()[0].GetDescription())

	assert.Equal(t, "from-specs-1", ciliumPolicy.GetSpecs()[1].GetEndpointSelector().GetMatchLabels()["app"])
	assert.Equal(t, "from-specs-2", ciliumPolicy.GetSpecs()[2].GetEndpointSelector().GetMatchLabels()["app"])
}

func TestConvertUnstructuredToCiliumPolicy_IngressFromCIDR(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "ingress-cidr-policy",
				"namespace":       "default",
				"uid":             "icidr-uid",
				"resourceVersion": "90909",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"ingress": []any{
					map[string]any{
						"fromCIDR": []any{"10.0.0.0/8", "172.16.0.0/12"},
						"fromCIDRSet": []any{
							map[string]any{
								"cidr":   "192.168.0.0/16",
								"except": []any{"192.168.1.0/24"},
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

	spec := result.GetCiliumNetworkPolicy().GetSpecs()[0]
	require.Len(t, spec.GetIngressRules(), 1)

	ingressRule := spec.GetIngressRules()[0]
	assert.ElementsMatch(t, []string{"10.0.0.0/8", "172.16.0.0/12"}, ingressRule.GetFromCidr())

	require.Len(t, ingressRule.GetFromCidrSet(), 1)
	assert.Equal(t, "192.168.0.0/16", ingressRule.GetFromCidrSet()[0].GetCidr())
	assert.ElementsMatch(t, []string{"192.168.1.0/24"}, ingressRule.GetFromCidrSet()[0].GetExcept())
}

func TestConvertUnstructuredToCiliumPolicy_ICMPTypeOutOfRange(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "icmp-range-policy",
				"namespace":       "default",
				"uid":             "icmprange-uid",
				"resourceVersion": "10110",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"ingress": []any{
					map[string]any{
						"icmps": []any{
							map[string]any{
								"fields": []any{
									map[string]any{
										"family": "IPv4",
										"type":   int64(999),
									},
									map[string]any{
										"family": "IPv4",
										"type":   int64(-1),
									},
									map[string]any{
										"family": "IPv4",
										"type":   float64(300),
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

	spec := result.GetCiliumNetworkPolicy().GetSpecs()[0]
	fields := spec.GetIngressRules()[0].GetIcmps()[0].GetFields()
	require.Len(t, fields, 3)

	for i, f := range fields {
		assert.Equal(t, "IPv4", f.GetFamily())
		assert.Nil(t, f.GetType(), "field[%d] with out-of-range value should have nil Type", i)
	}
}

func TestConvertUnstructuredToCiliumPolicy_EgressToGroups(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "egress-groups-policy",
				"namespace":       "default",
				"uid":             "egrp-uid",
				"resourceVersion": "20220",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"egress": []any{
					map[string]any{
						"toGroups": []any{
							map[string]any{
								"aws": map[string]any{
									"labels": map[string]any{
										"env": "staging",
									},
									"securityGroupsIds": []any{"sg-abcde"},
									"region":            "eu-west-1",
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

	spec := result.GetCiliumNetworkPolicy().GetSpecs()[0]
	require.Len(t, spec.GetEgressRules(), 1)

	egressRule := spec.GetEgressRules()[0]
	require.Len(t, egressRule.GetToGroups(), 1)

	awsGroup := egressRule.GetToGroups()[0].GetAws()
	require.NotNil(t, awsGroup)
	assert.Equal(t, "staging", awsGroup.GetLabels()["env"])
	assert.ElementsMatch(t, []string{"sg-abcde"}, awsGroup.GetSecurityGroupIds())
	assert.Equal(t, "eu-west-1", awsGroup.GetRegion())
}
