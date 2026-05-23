// Copyright 2026 Illumio, Inc. All Rights Reserved.

package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

func TestIsCiliumResource(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"CiliumNetworkPolicy kind", "CiliumNetworkPolicy", true},
		{"CiliumClusterwideNetworkPolicy kind", "CiliumClusterwideNetworkPolicy", true},
		{"CiliumCIDRGroup kind", "CiliumCIDRGroup", true},
		{"ciliumnetworkpolicies resource", "ciliumnetworkpolicies", true},
		{"ciliumclusterwidenetworkpolicies resource", "ciliumclusterwidenetworkpolicies", true},
		{"ciliumcidrgroups resource", "ciliumcidrgroups", true},
		{"NetworkPolicy is not Cilium", "NetworkPolicy", false},
		{"pods is not Cilium", "pods", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsCiliumResource(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertUnstructuredToCiliumResource_Nil(t *testing.T) {
	result, err := ConvertUnstructuredToCiliumResource(nil)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "cannot convert nil object")
}

func TestConvertUnstructuredToCiliumResource_Basic(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
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

	require.Len(t, spec.GetIngress(), 1)
	ingressRule := spec.GetIngress()[0]
	require.NotNil(t, ingressRule.GetFromEndpoints())
	require.Len(t, ingressRule.GetFromEndpoints().GetItems(), 1)
	assert.Equal(t, "frontend", ingressRule.GetFromEndpoints().GetItems()[0].GetMatchLabels()["role"])
}

func TestConvertUnstructuredToCiliumResource_ClusterwidePolicy(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
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

func TestConvertUnstructuredToCiliumResource_WithPorts(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetIngress(), 1)

	ingressRule := spec.GetIngress()[0]
	require.Len(t, ingressRule.GetToPorts(), 1)

	portRule := ingressRule.GetToPorts()[0]
	require.Len(t, portRule.GetPorts(), 2)
	assert.Equal(t, "80", portRule.GetPorts()[0].GetPort())
	assert.Equal(t, "TCP", portRule.GetPorts()[0].GetProtocol())
	assert.Equal(t, "443", portRule.GetPorts()[1].GetPort())
}

func TestConvertUnstructuredToCiliumResource_WithCIDR(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetEgress(), 1)

	egressRule := spec.GetEgress()[0]
	assert.ElementsMatch(t, []string{"10.0.0.0/8", "192.168.0.0/16"}, egressRule.GetToCidr())

	require.Len(t, egressRule.GetToCidrSet(), 1)
	assert.Equal(t, "172.16.0.0/12", egressRule.GetToCidrSet()[0].GetCidr())
	assert.ElementsMatch(t, []string{"172.16.1.0/24"}, egressRule.GetToCidrSet()[0].GetExcept())
}

func TestConvertUnstructuredToCiliumResource_MultipleSpecs(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
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

func TestConvertUnstructuredToCiliumResource_WithDefaultDeny(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
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

func TestConvertUnstructuredToCiliumResource_WithAWSGroups(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetIngress(), 1)

	ingressRule := spec.GetIngress()[0]
	require.Len(t, ingressRule.GetFromGroups(), 1)

	group := ingressRule.GetFromGroups()[0]
	awsGroup := group.GetAws()
	require.NotNil(t, awsGroup)
	assert.Equal(t, "production", awsGroup.GetLabels()["env"])
	assert.ElementsMatch(t, []string{"sg-12345", "sg-67890"}, awsGroup.GetSecurityGroupIds())
	assert.ElementsMatch(t, []string{"web-sg", "api-sg"}, awsGroup.GetSecurityGroupNames())
	assert.Equal(t, "us-west-2", awsGroup.GetRegion())
}

func TestConvertUnstructuredToCiliumResource_WithNodeSelectors(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]

	require.Len(t, spec.GetIngress(), 1)
	require.Len(t, spec.GetIngress()[0].GetFromNodes(), 1)
	assert.Equal(t, "worker", spec.GetIngress()[0].GetFromNodes()[0].GetMatchLabels()["node-type"])

	require.Len(t, spec.GetEgress(), 1)
	require.Len(t, spec.GetEgress()[0].GetToNodes(), 1)
	assert.Equal(t, "control-plane", spec.GetEgress()[0].GetToNodes()[0].GetMatchLabels()["node-type"])
}

func TestConvertUnstructuredToCiliumResource_WithICMP(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetIngress(), 1)

	ingressRule := spec.GetIngress()[0]
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

func TestConvertUnstructuredToCiliumResource_WithAuthentication(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetIngress(), 1)

	ingressRule := spec.GetIngress()[0]
	require.NotNil(t, ingressRule.GetAuthentication())
	assert.Equal(t, "required", ingressRule.GetAuthentication().GetMode())
}

func TestConvertUnstructuredToCiliumResource_WithFQDN(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetEgress(), 1)

	egressRule := spec.GetEgress()[0]
	require.Len(t, egressRule.GetToFqdns(), 2)

	assert.Equal(t, "api.example.com", egressRule.GetToFqdns()[0].GetMatchName())
	assert.Equal(t, "*.internal.example.com", egressRule.GetToFqdns()[1].GetMatchPattern())
}

func TestConvertUnstructuredToCiliumResource_WithK8sServices(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetEgress(), 1)

	egressRule := spec.GetEgress()[0]
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

func TestConvertUnstructuredToCiliumResource_WithCIDRGroupRef(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetEgress(), 1)

	egressRule := spec.GetEgress()[0]
	require.Len(t, egressRule.GetToCidrSet(), 2)

	assert.Equal(t, "office-networks", egressRule.GetToCidrSet()[0].GetCidrGroupRef())

	cidrGroupSelector := egressRule.GetToCidrSet()[1].GetCidrGroupSelector()
	require.NotNil(t, cidrGroupSelector)
	assert.Equal(t, "trusted", cidrGroupSelector.GetMatchLabels()["network-type"])
}

func TestConvertUnstructuredToCiliumResource_WithEndPort(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetIngress(), 1)

	ingressRule := spec.GetIngress()[0]
	require.Len(t, ingressRule.GetToPorts(), 1)

	portRule := ingressRule.GetToPorts()[0]
	require.Len(t, portRule.GetPorts(), 1)

	port := portRule.GetPorts()[0]
	assert.Equal(t, "8000", port.GetPort())
	assert.Equal(t, int32(8100), port.GetEndPort())
	assert.Equal(t, "TCP", port.GetProtocol())
}

func TestConvertUnstructuredToCiliumResource_WithDenyRules(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]

	require.Len(t, spec.GetIngressDeny(), 1)
	require.NotNil(t, spec.GetIngressDeny()[0].GetFromEndpoints())
	assert.Equal(t, "untrusted", spec.GetIngressDeny()[0].GetFromEndpoints().GetItems()[0].GetMatchLabels()["app"])

	require.Len(t, spec.GetEgressDeny(), 1)
	assert.ElementsMatch(t, []string{"world"}, spec.GetEgressDeny()[0].GetToEntities())
}

func TestConvertUnstructuredToCiliumResource_WithLabels(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	assert.Equal(t, "compliance", spec.GetLabels()["policy-type"])
	assert.Equal(t, "security-team", spec.GetLabels()["owner"])
}

func TestConvertUnstructuredToCiliumResource_WithMatchExpressions(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
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

func TestConvertUnstructuredToCiliumResource_ComplexEgress(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	require.Len(t, ciliumPolicy.GetSpecs(), 1)

	spec := ciliumPolicy.GetSpecs()[0]
	require.Len(t, spec.GetEgress(), 1)

	egressRule := spec.GetEgress()[0]

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

func TestConvertUnstructuredToCiliumResource_CombinedSpecAndSpecs(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
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

func TestConvertUnstructuredToCiliumResource_IngressFromCIDR(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)

	spec := result.GetCiliumNetworkPolicy().GetSpecs()[0]
	require.Len(t, spec.GetIngress(), 1)

	ingressRule := spec.GetIngress()[0]
	assert.ElementsMatch(t, []string{"10.0.0.0/8", "172.16.0.0/12"}, ingressRule.GetFromCidr())

	require.Len(t, ingressRule.GetFromCidrSet(), 1)
	assert.Equal(t, "192.168.0.0/16", ingressRule.GetFromCidrSet()[0].GetCidr())
	assert.ElementsMatch(t, []string{"192.168.1.0/24"}, ingressRule.GetFromCidrSet()[0].GetExcept())
}

func TestConvertUnstructuredToCiliumResource_ICMPTypeOutOfRange(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)

	spec := result.GetCiliumNetworkPolicy().GetSpecs()[0]
	fields := spec.GetIngress()[0].GetIcmps()[0].GetFields()
	require.Len(t, fields, 3)

	for i, f := range fields {
		assert.Equal(t, "IPv4", f.GetFamily())
		assert.Nil(t, f.GetType(), "field[%d] with out-of-range value should have nil Type", i)
	}
}

func TestConvertUnstructuredToCiliumResource_EgressToGroups(t *testing.T) {
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)

	spec := result.GetCiliumNetworkPolicy().GetSpecs()[0]
	require.Len(t, spec.GetEgress(), 1)

	egressRule := spec.GetEgress()[0]
	require.Len(t, egressRule.GetToGroups(), 1)

	awsGroup := egressRule.GetToGroups()[0].GetAws()
	require.NotNil(t, awsGroup)
	assert.Equal(t, "staging", awsGroup.GetLabels()["env"])
	assert.ElementsMatch(t, []string{"sg-abcde"}, awsGroup.GetSecurityGroupIds())
	assert.Equal(t, "eu-west-1", awsGroup.GetRegion())
}

func TestConvertUnstructuredToCiliumResource_EmptyPolicy(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "empty-policy",
				"namespace":       "default",
				"uid":             "empty-uid",
				"resourceVersion": "11111",
			},
		},
	}

	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2",
		Kind:    "CiliumNetworkPolicy",
	})

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ciliumPolicy := result.GetCiliumNetworkPolicy()
	require.NotNil(t, ciliumPolicy)
	assert.Empty(t, ciliumPolicy.GetSpecs(), "policy with no spec or specs should have empty specs")
}

func TestConvertUnstructuredToCiliumResource_MetadataFields(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":              "metadata-policy",
				"namespace":         "prod",
				"uid":               "meta-uid-123",
				"resourceVersion":   "99999",
				"creationTimestamp": "2026-05-01T12:00:00Z",
				"annotations": map[string]any{
					"policy.cilium.io/description": "test annotation",
				},
				"labels": map[string]any{
					"team": "platform",
				},
				"ownerReferences": []any{
					map[string]any{
						"apiVersion":         "apps/v1",
						"kind":               "Deployment",
						"name":               "my-deployment",
						"uid":                "owner-uid-456",
						"controller":         true,
						"blockOwnerDeletion": true,
					},
				},
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
			},
		},
	}

	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2",
		Kind:    "CiliumNetworkPolicy",
	})

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "metadata-policy", result.GetName())
	assert.Equal(t, "prod", result.GetNamespace())
	assert.Equal(t, "meta-uid-123", result.GetUid())
	assert.Equal(t, "99999", result.GetResourceVersion())

	assert.Equal(t, "test annotation", result.GetAnnotations()["policy.cilium.io/description"])
	assert.Equal(t, "platform", result.GetLabels()["team"])

	require.NotNil(t, result.GetCreationTimestamp())
	assert.Equal(t, int64(2026), int64(result.GetCreationTimestamp().AsTime().Year()))

	require.Len(t, result.GetOwnerReferences(), 1)
	ownerRef := result.GetOwnerReferences()[0]
	assert.Equal(t, "apps/v1", ownerRef.GetApiVersion())
	assert.Equal(t, "Deployment", ownerRef.GetKind())
	assert.Equal(t, "my-deployment", ownerRef.GetName())
	assert.Equal(t, "owner-uid-456", ownerRef.GetUid())
	assert.True(t, ownerRef.GetController())
	assert.True(t, ownerRef.GetBlockOwnerDeletion())
}

func TestConvertUnstructuredToCiliumResource_IngressFromEntities(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "entity-ingress-policy",
				"namespace":       "default",
				"uid":             "entity-uid",
				"resourceVersion": "22222",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"ingress": []any{
					map[string]any{
						"fromEntities": []any{"world", "host", "remote-node"},
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)

	spec := result.GetCiliumNetworkPolicy().GetSpecs()[0]
	require.Len(t, spec.GetIngress(), 1)

	ingressRule := spec.GetIngress()[0]
	assert.ElementsMatch(t, []string{"world", "host", "remote-node"}, ingressRule.GetFromEntities())
}

func TestConvertUnstructuredToCiliumResource_UnsupportedKind(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumEgressGatewayPolicy",
			"metadata": map[string]any{
				"name": "test-policy",
			},
		},
	}

	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2",
		Kind:    "CiliumEgressGatewayPolicy",
	})

	_, err := ConvertUnstructuredToCiliumResource(obj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Cilium resource kind")
	assert.Contains(t, err.Error(), "CiliumEgressGatewayPolicy")
}

func TestConvertUnstructuredToCiliumResource_CIDRGroup(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2alpha1",
			"kind":       "CiliumCIDRGroup",
			"metadata": map[string]any{
				"name":            "office-networks",
				"uid":             "cidrgroup-uid",
				"resourceVersion": "33333",
			},
			"spec": map[string]any{
				"externalCIDRs": []any{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
			},
		},
	}

	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2alpha1",
		Kind:    "CiliumCIDRGroup",
	})

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "office-networks", result.GetName())
	assert.Equal(t, "CiliumCIDRGroup", result.GetKind())
	assert.Empty(t, result.GetNamespace(), "CIDRGroup is cluster-scoped")

	cidrGroup := result.GetCiliumCidrGroup()
	require.NotNil(t, cidrGroup)
	require.NotNil(t, cidrGroup.GetSpec())
	assert.ElementsMatch(t, []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}, cidrGroup.GetSpec().GetExternalCidrs())
}

func TestConvertUnstructuredToCiliumResource_CIDRGroupEmpty(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2alpha1",
			"kind":       "CiliumCIDRGroup",
			"metadata": map[string]any{
				"name":            "empty-cidrgroup",
				"uid":             "empty-cidrgroup-uid",
				"resourceVersion": "44444",
			},
			"spec": map[string]any{},
		},
	}

	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2alpha1",
		Kind:    "CiliumCIDRGroup",
	})

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	cidrGroup := result.GetCiliumCidrGroup()
	require.NotNil(t, cidrGroup)
	require.NotNil(t, cidrGroup.GetSpec())
	assert.Empty(t, cidrGroup.GetSpec().GetExternalCidrs())
}

func TestConvertUnstructuredToCiliumResource_MalformedJSON(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name": "malformed-policy",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{
					"matchLabels": "not-a-map",
				},
			},
		},
	}

	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2",
		Kind:    "CiliumNetworkPolicy",
	})

	_, err := ConvertUnstructuredToCiliumResource(obj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deserializing")
}

func TestConvertUnstructuredToCiliumResource_DenyRulesWithPorts(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "deny-ports-policy",
				"namespace":       "default",
				"uid":             "denyports-uid",
				"resourceVersion": "50505",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"ingressDeny": []any{
					map[string]any{
						"fromEndpoints": []any{
							map[string]any{
								"matchLabels": map[string]any{"app": "blocked"},
							},
						},
						"toPorts": []any{
							map[string]any{
								"ports": []any{
									map[string]any{
										"port":     "22",
										"protocol": "TCP",
									},
								},
							},
						},
					},
				},
				"egressDeny": []any{
					map[string]any{
						"toEntities": []any{"world"},
						"toPorts": []any{
							map[string]any{
								"ports": []any{
									map[string]any{
										"port":     "25",
										"protocol": "TCP",
									},
									map[string]any{
										"port":     "587",
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)

	spec := result.GetCiliumNetworkPolicy().GetSpecs()[0]

	require.Len(t, spec.GetIngressDeny(), 1)
	ingressDeny := spec.GetIngressDeny()[0]
	require.Len(t, ingressDeny.GetToPorts(), 1)
	require.Len(t, ingressDeny.GetToPorts()[0].GetPorts(), 1)
	assert.Equal(t, "22", ingressDeny.GetToPorts()[0].GetPorts()[0].GetPort())
	assert.Equal(t, "TCP", ingressDeny.GetToPorts()[0].GetPorts()[0].GetProtocol())

	require.Len(t, spec.GetEgressDeny(), 1)
	egressDeny := spec.GetEgressDeny()[0]
	require.Len(t, egressDeny.GetToPorts(), 1)
	require.Len(t, egressDeny.GetToPorts()[0].GetPorts(), 2)
	assert.Equal(t, "25", egressDeny.GetToPorts()[0].GetPorts()[0].GetPort())
	assert.Equal(t, "587", egressDeny.GetToPorts()[0].GetPorts()[1].GetPort())
}

func TestConvertUnstructuredToCiliumResource_DenyRulesWithICMP(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "deny-icmp-policy",
				"namespace":       "default",
				"uid":             "denyicmp-uid",
				"resourceVersion": "60606",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"ingressDeny": []any{
					map[string]any{
						"icmps": []any{
							map[string]any{
								"fields": []any{
									map[string]any{
										"family": "IPv4",
										"type":   int64(8),
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)

	spec := result.GetCiliumNetworkPolicy().GetSpecs()[0]
	require.Len(t, spec.GetIngressDeny(), 1)

	ingressDeny := spec.GetIngressDeny()[0]
	require.Len(t, ingressDeny.GetIcmps(), 1)
	require.Len(t, ingressDeny.GetIcmps()[0].GetFields(), 1)
	assert.Equal(t, "IPv4", ingressDeny.GetIcmps()[0].GetFields()[0].GetFamily())
	assert.Equal(t, uint32(8), ingressDeny.GetIcmps()[0].GetFields()[0].GetTypeInt())
}

func TestConvertUnstructuredToCiliumResource_CIDRGroupV2(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumCIDRGroup",
			"metadata": map[string]any{
				"name":            "v2-cidrgroup",
				"uid":             "v2cidrgroup-uid",
				"resourceVersion": "70707",
			},
			"spec": map[string]any{
				"externalCIDRs": []any{"10.0.0.0/8"},
			},
		},
	}

	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2",
		Kind:    "CiliumCIDRGroup",
	})

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "v2", result.GetApiVersion())
	assert.Equal(t, "CiliumCIDRGroup", result.GetKind())

	cidrGroup := result.GetCiliumCidrGroup()
	require.NotNil(t, cidrGroup)
	assert.ElementsMatch(t, []string{"10.0.0.0/8"}, cidrGroup.GetSpec().GetExternalCidrs())
}

func TestConvertUnstructuredToCiliumResource_EgressAuthentication(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "egress-auth-policy",
				"namespace":       "default",
				"uid":             "egressauth-uid",
				"resourceVersion": "80808",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"egress": []any{
					map[string]any{
						"toEndpoints": []any{
							map[string]any{
								"matchLabels": map[string]any{"app": "backend"},
							},
						},
						"authentication": map[string]any{
							"mode": "always-fail",
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)

	spec := result.GetCiliumNetworkPolicy().GetSpecs()[0]
	require.Len(t, spec.GetEgress(), 1)

	egressRule := spec.GetEgress()[0]
	require.NotNil(t, egressRule.GetAuthentication())
	assert.Equal(t, "always-fail", egressRule.GetAuthentication().GetMode())
}

func TestConvertUnstructuredToCiliumResource_LabelArrayEmptyKey(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "empty-key-label-policy",
				"namespace":       "default",
				"uid":             "emptykey-uid",
				"resourceVersion": "90909",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"labels": []any{
					map[string]any{"key": "valid-key", "value": "valid-value"},
					map[string]any{"key": "", "value": "should-be-rejected"},
					map[string]any{"key": "another-key", "value": "another-value"},
				},
			},
		},
	}

	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2",
		Kind:    "CiliumNetworkPolicy",
	})

	_, err := ConvertUnstructuredToCiliumResource(obj)
	require.Error(t, err, "Cilium rejects labels with empty keys during deserialization")
	assert.Contains(t, err.Error(), "does not contain label key")
}

func TestConvertUnstructuredToCiliumResource_DefaultDenyPartialIngress(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "partial-deny-policy",
				"namespace":       "default",
				"uid":             "partialdeny-uid",
				"resourceVersion": "10101",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"enableDefaultDeny": map[string]any{
					"ingress": true,
				},
			},
		},
	}

	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2",
		Kind:    "CiliumNetworkPolicy",
	})

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)

	spec := result.GetCiliumNetworkPolicy().GetSpecs()[0]
	require.NotNil(t, spec.GetEnableDefaultDeny())
	assert.True(t, spec.GetEnableDefaultDeny().GetIngress())
	assert.False(t, spec.GetEnableDefaultDeny().GetEgress(), "unset egress should default to false")
}

func TestConvertUnstructuredToCiliumResource_ICMPNegativeIntegerString(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "icmp-neg-string-policy",
				"namespace":       "default",
				"uid":             "icmpneg-uid",
				"resourceVersion": "20202",
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
										"type":   "-1",
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)

	spec := result.GetCiliumNetworkPolicy().GetSpecs()[0]
	fields := spec.GetIngress()[0].GetIcmps()[0].GetFields()
	require.Len(t, fields, 1)

	assert.Equal(t, "IPv4", fields[0].GetFamily())
	assert.Nil(t, fields[0].GetType(), "negative integer string should be dropped, consistent with negative int behavior")
}

func TestConvertUnstructuredToCiliumResource_EgressDenyWithICMP(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]any{
				"name":            "egress-deny-icmp-policy",
				"namespace":       "default",
				"uid":             "egressdenyicmp-uid",
				"resourceVersion": "30303",
			},
			"spec": map[string]any{
				"endpointSelector": map[string]any{},
				"egressDeny": []any{
					map[string]any{
						"toEntities": []any{"world"},
						"toPorts": []any{
							map[string]any{
								"ports": []any{
									map[string]any{
										"port":     "443",
										"protocol": "TCP",
									},
								},
							},
						},
						"icmps": []any{
							map[string]any{
								"fields": []any{
									map[string]any{
										"family": "IPv4",
										"type":   int64(8),
									},
									map[string]any{
										"family": "IPv6",
										"type":   "EchoReply",
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

	result, err := ConvertUnstructuredToCiliumResource(obj)
	require.NoError(t, err)

	spec := result.GetCiliumNetworkPolicy().GetSpecs()[0]
	require.Len(t, spec.GetEgressDeny(), 1)

	egressDeny := spec.GetEgressDeny()[0]
	assert.ElementsMatch(t, []string{"world"}, egressDeny.GetToEntities())

	require.Len(t, egressDeny.GetToPorts(), 1)
	assert.Equal(t, "443", egressDeny.GetToPorts()[0].GetPorts()[0].GetPort())

	require.Len(t, egressDeny.GetIcmps(), 1)
	require.Len(t, egressDeny.GetIcmps()[0].GetFields(), 2)
	assert.Equal(t, "IPv4", egressDeny.GetIcmps()[0].GetFields()[0].GetFamily())
	assert.Equal(t, uint32(8), egressDeny.GetIcmps()[0].GetFields()[0].GetTypeInt())
	assert.Equal(t, "IPv6", egressDeny.GetIcmps()[0].GetFields()[1].GetFamily())
	assert.Equal(t, "EchoReply", egressDeny.GetIcmps()[0].GetFields()[1].GetTypeString())
}

func TestProtoToMap_NilMessage(t *testing.T) {
	// protojson marshals nil proto message to "{}" which unmarshals to empty map
	result, err := protoToMap(nil)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestProtoToMap_EmptyMessage(t *testing.T) {
	msg := &pb.CiliumPolicyRule{}
	result, err := protoToMap(msg)
	require.NoError(t, err)

	// Empty message should produce empty map (EmitUnpopulated=false)
	assert.Empty(t, result)
}

func TestProtoToMap_PreservesTypes(t *testing.T) {
	boolTrue := true

	msg := &pb.CiliumPolicyDefaultDeny{
		Ingress: &boolTrue,
	}

	result, err := protoToMap(msg)
	require.NoError(t, err)

	// Boolean should stay boolean, not become string
	ingress, ok := result["ingress"]
	require.True(t, ok)
	assert.IsType(t, true, ingress, "boolean should be preserved as bool, not string")
}

func TestMarshalPolicySpecs_SingleSpec(t *testing.T) {
	specs := []*pb.CiliumPolicyRule{
		{
			EndpointSelector: &pb.LabelSelector{
				MatchLabels: map[string]string{"app": "web"},
			},
		},
	}

	result, err := marshalPolicySpecs(specs)
	require.NoError(t, err)

	// Single spec should produce "spec" key, not "specs"
	_, hasSpec := result["spec"]
	_, hasSpecs := result["specs"]
	assert.True(t, hasSpec, "single spec should use 'spec' key")
	assert.False(t, hasSpecs, "single spec should not use 'specs' key")
}

func TestMarshalPolicySpecs_MultipleSpecs(t *testing.T) {
	specs := []*pb.CiliumPolicyRule{
		{EndpointSelector: &pb.LabelSelector{MatchLabels: map[string]string{"app": "a"}}},
		{EndpointSelector: &pb.LabelSelector{MatchLabels: map[string]string{"app": "b"}}},
	}

	result, err := marshalPolicySpecs(specs)
	require.NoError(t, err)

	// Multiple specs should produce "specs" key, not "spec"
	_, hasSpec := result["spec"]
	_, hasSpecs := result["specs"]
	assert.False(t, hasSpec, "multiple specs should not use 'spec' key")
	assert.True(t, hasSpecs, "multiple specs should use 'specs' key")

	specsList, ok := result["specs"].([]any)
	require.True(t, ok)
	assert.Len(t, specsList, 2)
}

func TestMarshalPolicySpecs_Empty(t *testing.T) {
	result, err := marshalPolicySpecs([]*pb.CiliumPolicyRule{})
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestMarshalPolicySpecs_Nil(t *testing.T) {
	result, err := marshalPolicySpecs(nil)
	require.NoError(t, err)
	assert.Empty(t, result)
}
