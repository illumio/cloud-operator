// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestIsCiliumPolicy(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		// Valid Kind formats (PascalCase, singular)
		{name: "CiliumNetworkPolicy kind", input: "CiliumNetworkPolicy", expected: true},
		{name: "CiliumClusterwideNetworkPolicy kind", input: "CiliumClusterwideNetworkPolicy", expected: true},

		// Valid resource name formats (lowercase, plural)
		{name: "ciliumnetworkpolicies resource", input: "ciliumnetworkpolicies", expected: true},
		{name: "ciliumclusterwidenetworkpolicies resource", input: "ciliumclusterwidenetworkpolicies", expected: true},

		// Invalid inputs
		{name: "empty string", input: "", expected: false},
		{name: "NetworkPolicy", input: "NetworkPolicy", expected: false},
		{name: "networkpolicies", input: "networkpolicies", expected: false},
		{name: "Pod", input: "Pod", expected: false},
		{name: "random string", input: "random", expected: false},
		{name: "lowercase kind", input: "ciliumnetworkpolicy", expected: false},
		{name: "uppercase resource", input: "CILIUMNETWORKPOLICIES", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsCiliumPolicy(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertUnstructuredToCiliumPolicy_NilInput(t *testing.T) {
	result, err := ConvertUnstructuredToCiliumPolicy(nil)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot convert nil object")
}

func TestConvertUnstructuredToCiliumPolicy_UnsupportedKind(t *testing.T) {
	obj := &unstructured.Unstructured{}
	obj.SetKind("UnsupportedKind")
	obj.SetName("test-policy")
	obj.SetNamespace("default")

	result, err := ConvertUnstructuredToCiliumPolicy(obj)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Cilium policy kind")
}

func TestConvertUnstructuredToCiliumPolicy_CiliumNetworkPolicy(t *testing.T) {
	obj := newTestCiliumNetworkPolicy("test-policy", "default", map[string]any{
		"endpointSelector": map[string]any{
			"matchLabels": map[string]any{
				"app": "web",
			},
		},
		"ingress": []any{
			map[string]any{
				"fromEndpoints": []any{
					map[string]any{
						"matchLabels": map[string]any{
							"app": "backend",
						},
					},
				},
				"toPorts": []any{
					map[string]any{
						"ports": []any{
							map[string]any{
								"port":     "80",
								"protocol": "TCP",
							},
						},
					},
				},
			},
		},
	})

	result, err := ConvertUnstructuredToCiliumPolicy(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "CiliumNetworkPolicy", result.GetKind())
	assert.Equal(t, "test-policy", result.GetName())
	assert.Equal(t, "default", result.GetNamespace())

	// Verify kind_specific is set correctly
	cnp := result.GetCiliumNetworkPolicy()
	require.NotNil(t, cnp)
	require.Len(t, cnp.GetSpecs(), 1)

	// Verify endpoint selector
	spec := cnp.GetSpecs()[0]
	require.NotNil(t, spec.GetEndpointSelector())
	assert.Equal(t, "web", spec.GetEndpointSelector().GetMatchLabels()["app"])

	// Verify ingress rules
	require.Len(t, spec.GetIngressRules(), 1)
	ingressRule := spec.GetIngressRules()[0]
	require.NotNil(t, ingressRule.GetFromEndpoints())
	require.Len(t, ingressRule.GetFromEndpoints().GetItems(), 1)
	assert.Equal(t, "backend", ingressRule.GetFromEndpoints().GetItems()[0].GetMatchLabels()["app"])

	// Verify ports
	require.Len(t, ingressRule.GetToPorts(), 1)
	require.Len(t, ingressRule.GetToPorts()[0].GetPorts(), 1)
	assert.Equal(t, "80", ingressRule.GetToPorts()[0].GetPorts()[0].GetPort())
	assert.Equal(t, "TCP", ingressRule.GetToPorts()[0].GetPorts()[0].GetProtocol())
}

func TestConvertUnstructuredToCiliumPolicy_CiliumClusterwideNetworkPolicy(t *testing.T) {
	obj := newTestCiliumClusterwideNetworkPolicy("test-ccnp", map[string]any{
		"nodeSelector": map[string]any{
			"matchLabels": map[string]any{
				"node-type": "worker",
			},
		},
		"ingress": []any{
			map[string]any{
				"fromEntities": []any{"cluster"},
			},
		},
	})

	result, err := ConvertUnstructuredToCiliumPolicy(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "CiliumClusterwideNetworkPolicy", result.GetKind())
	assert.Equal(t, "test-ccnp", result.GetName())
	assert.Empty(t, result.GetNamespace()) // Cluster-scoped

	// Verify kind_specific is set correctly
	ccnp := result.GetCiliumClusterwideNetworkPolicy()
	require.NotNil(t, ccnp)
	require.Len(t, ccnp.GetSpecs(), 1)

	// Verify node selector
	spec := ccnp.GetSpecs()[0]
	require.NotNil(t, spec.GetNodeSelector())
	assert.Equal(t, "worker", spec.GetNodeSelector().GetMatchLabels()["node-type"])
}

func TestCollectCiliumSpecs_SpecOnly(t *testing.T) {
	obj := map[string]any{
		"spec": map[string]any{
			"endpointSelector": map[string]any{
				"matchLabels": map[string]any{"app": "test"},
			},
		},
	}

	specs, err := collectCiliumSpecs(obj, "cilium.io/v2")
	require.NoError(t, err)
	require.Len(t, specs, 1)
	assert.Equal(t, "test", specs[0].GetEndpointSelector().GetMatchLabels()["app"])
}

func TestCollectCiliumSpecs_SpecsOnly(t *testing.T) {
	obj := map[string]any{
		"specs": []any{
			map[string]any{
				"endpointSelector": map[string]any{
					"matchLabels": map[string]any{"app": "spec1"},
				},
			},
			map[string]any{
				"endpointSelector": map[string]any{
					"matchLabels": map[string]any{"app": "spec2"},
				},
			},
		},
	}

	specs, err := collectCiliumSpecs(obj, "cilium.io/v2")
	require.NoError(t, err)
	require.Len(t, specs, 2)
	assert.Equal(t, "spec1", specs[0].GetEndpointSelector().GetMatchLabels()["app"])
	assert.Equal(t, "spec2", specs[1].GetEndpointSelector().GetMatchLabels()["app"])
}

func TestCollectCiliumSpecs_BothSpecAndSpecs(t *testing.T) {
	obj := map[string]any{
		"spec": map[string]any{
			"endpointSelector": map[string]any{
				"matchLabels": map[string]any{"app": "from-spec"},
			},
		},
		"specs": []any{
			map[string]any{
				"endpointSelector": map[string]any{
					"matchLabels": map[string]any{"app": "from-specs"},
				},
			},
		},
	}

	specs, err := collectCiliumSpecs(obj, "cilium.io/v2")
	require.NoError(t, err)
	require.Len(t, specs, 2)
	assert.Equal(t, "from-spec", specs[0].GetEndpointSelector().GetMatchLabels()["app"])
	assert.Equal(t, "from-specs", specs[1].GetEndpointSelector().GetMatchLabels()["app"])
}

func TestCollectCiliumSpecs_Empty(t *testing.T) {
	obj := map[string]any{}

	specs, err := collectCiliumSpecs(obj, "cilium.io/v2")
	require.NoError(t, err)
	assert.Empty(t, specs)
}

func TestCollectCiliumSpecs_InvalidSpecType(t *testing.T) {
	// spec should be a map, not a string
	obj := map[string]any{
		"spec": "invalid-not-a-map",
	}

	specs, err := collectCiliumSpecs(obj, "cilium.io/v2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid spec field type")
	assert.Nil(t, specs)
}

func TestCollectCiliumSpecs_InvalidSpecsType(t *testing.T) {
	// specs should be a slice, not a string
	obj := map[string]any{
		"specs": "invalid-not-a-slice",
	}

	specs, err := collectCiliumSpecs(obj, "cilium.io/v2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid specs field type")
	assert.Nil(t, specs)
}

func TestConvertCiliumPorts_NumericPort(t *testing.T) {
	// When YAML has `port: 80` (numeric), it becomes float64 in unstructured
	ports := []any{
		map[string]any{
			"port":     float64(80),
			"protocol": "TCP",
		},
	}

	result := convertCiliumPorts(ports)
	require.Len(t, result, 1)
	assert.Equal(t, "80", result[0].GetPort())
	assert.Equal(t, "TCP", result[0].GetProtocol())
}

func TestConvertCiliumPorts_StringPort(t *testing.T) {
	ports := []any{
		map[string]any{
			"port":     "http",
			"protocol": "TCP",
		},
	}

	result := convertCiliumPorts(ports)
	require.Len(t, result, 1)
	assert.Equal(t, "http", result[0].GetPort())
}

func TestConvertCiliumPorts_PortRange(t *testing.T) {
	ports := []any{
		map[string]any{
			"port":     "8080",
			"endPort":  int64(8090),
			"protocol": "TCP",
		},
	}

	result := convertCiliumPorts(ports)
	require.Len(t, result, 1)
	assert.Equal(t, "8080", result[0].GetPort())
	require.NotNil(t, result[0].EndPort)
	assert.Equal(t, int32(8090), result[0].GetEndPort())
}

func TestConvertCiliumGroups_AWSSecurityGroups(t *testing.T) {
	groups := []any{
		map[string]any{
			"aws": map[string]any{
				"region":              "us-west-2",
				"securityGroupsIds":   []any{"sg-123", "sg-456"},
				"securityGroupsNames": []any{"sg-name-1"},
				"labels":              map[string]any{"env": "prod"},
			},
		},
	}

	result := convertCiliumGroups(groups)
	require.Len(t, result, 1)

	// Access AWS fields via oneof accessor
	aws := result[0].GetAws()
	require.NotNil(t, aws, "expected AWS cloud provider")

	require.NotNil(t, aws.Region)
	assert.Equal(t, "us-west-2", aws.GetRegion())

	require.Len(t, aws.GetSecurityGroupIds(), 2)
	assert.Equal(t, "sg-123", aws.GetSecurityGroupIds()[0])
	assert.Equal(t, "sg-456", aws.GetSecurityGroupIds()[1])

	require.Len(t, aws.GetSecurityGroupNames(), 1)
	assert.Equal(t, "sg-name-1", aws.GetSecurityGroupNames()[0])

	require.Len(t, aws.GetLabels(), 1)
	assert.Equal(t, "prod", aws.GetLabels()["env"])
}

func TestConvertCiliumIngressRules(t *testing.T) {
	rules := []any{
		map[string]any{
			"fromEndpoints": []any{
				map[string]any{
					"matchLabels": map[string]any{"app": "backend"},
				},
			},
			"fromCIDR":     []any{"10.0.0.0/8"},
			"fromEntities": []any{"world", "cluster"},
			"toPorts": []any{
				map[string]any{
					"ports": []any{
						map[string]any{"port": "443", "protocol": "TCP"},
					},
				},
			},
		},
	}

	result, err := convertCiliumIngressRules(rules)
	require.NoError(t, err)
	require.Len(t, result, 1)

	rule := result[0]

	// FromEndpoints
	require.NotNil(t, rule.GetFromEndpoints())
	require.Len(t, rule.GetFromEndpoints().GetItems(), 1)
	assert.Equal(t, "backend", rule.GetFromEndpoints().GetItems()[0].GetMatchLabels()["app"])

	// FromCIDR
	require.Len(t, rule.GetFromCidr(), 1)
	assert.Equal(t, "10.0.0.0/8", rule.GetFromCidr()[0])

	// FromEntities
	require.Len(t, rule.GetFromEntities(), 2)
	assert.Contains(t, rule.GetFromEntities(), "world")
	assert.Contains(t, rule.GetFromEntities(), "cluster")

	// ToPorts
	require.Len(t, rule.GetToPorts(), 1)
	require.Len(t, rule.GetToPorts()[0].GetPorts(), 1)
	assert.Equal(t, "443", rule.GetToPorts()[0].GetPorts()[0].GetPort())
}

func TestConvertCiliumEgressRules(t *testing.T) {
	rules := []any{
		map[string]any{
			"toEndpoints": []any{
				map[string]any{
					"matchLabels": map[string]any{"app": "database"},
				},
			},
			"toCIDR": []any{"0.0.0.0/0"},
			"toFQDNs": []any{
				map[string]any{"matchName": "api.example.com"},
				map[string]any{"matchPattern": "*.internal.com"},
			},
			"toEntities": []any{"world"},
		},
	}

	result, err := convertCiliumEgressRules(rules)
	require.NoError(t, err)
	require.Len(t, result, 1)

	rule := result[0]

	// ToEndpoints
	require.NotNil(t, rule.GetToEndpoints())
	require.Len(t, rule.GetToEndpoints().GetItems(), 1)
	assert.Equal(t, "database", rule.GetToEndpoints().GetItems()[0].GetMatchLabels()["app"])

	// ToCIDR
	require.Len(t, rule.GetToCidr(), 1)
	assert.Equal(t, "0.0.0.0/0", rule.GetToCidr()[0])

	// ToFQDNs (oneof: either MatchName or MatchPattern)
	require.Len(t, rule.GetToFqdns(), 2)
	assert.Equal(t, "api.example.com", rule.GetToFqdns()[0].GetMatchName())
	assert.Equal(t, "*.internal.com", rule.GetToFqdns()[1].GetMatchPattern())

	// ToEntities
	require.Len(t, rule.GetToEntities(), 1)
	assert.Equal(t, "world", rule.GetToEntities()[0])
}

func TestConvertCiliumCIDRSets(t *testing.T) {
	cidrSets := []any{
		map[string]any{
			"cidr":   "10.0.0.0/8",
			"except": []any{"10.1.0.0/16", "10.2.0.0/16"},
		},
		map[string]any{
			"cidrGroupRef": "my-cidr-group",
		},
	}

	result := convertCiliumCIDRSets(cidrSets)
	require.Len(t, result, 2)

	// First CIDR set with exceptions
	assert.Equal(t, "10.0.0.0/8", result[0].GetCidr())
	require.Len(t, result[0].GetExcept(), 2)
	assert.Contains(t, result[0].GetExcept(), "10.1.0.0/16")
	assert.Contains(t, result[0].GetExcept(), "10.2.0.0/16")

	// Second CIDR set with group reference
	require.NotNil(t, result[1].GetCidrGroupRef())
	assert.Equal(t, "my-cidr-group", result[1].GetCidrGroupRef())
}

func TestConvertCiliumICMPRules(t *testing.T) {
	icmps := []any{
		map[string]any{
			"fields": []any{
				map[string]any{
					"type":   int64(8),
					"family": "IPv4",
				},
			},
		},
	}

	result := convertCiliumICMPRules(icmps)
	require.Len(t, result, 1)
	require.Len(t, result[0].GetFields(), 1)

	field := result[0].GetFields()[0]
	assert.Equal(t, uint32(8), field.GetTypeInt())
	require.NotNil(t, field.Family)
	assert.Equal(t, "IPv4", field.GetFamily())
}

func TestConvertCiliumAuthentication(t *testing.T) {
	auth := map[string]any{
		"mode": "required",
	}

	result := convertCiliumAuthentication(auth)
	require.NotNil(t, result)
	assert.Equal(t, "required", result.GetMode())
}

func TestConvertCiliumAuthentication_Nil(t *testing.T) {
	result := convertCiliumAuthentication(nil)
	assert.Nil(t, result)
}

func TestConvertCiliumServices(t *testing.T) {
	services := []any{
		map[string]any{
			"k8sService": map[string]any{
				"serviceName": "my-service",
				"namespace":   "default",
			},
		},
		map[string]any{
			// Cilium's k8sServiceSelector nests the label selector under "selector"
			"k8sServiceSelector": map[string]any{
				"selector": map[string]any{
					"matchLabels": map[string]any{
						"app": "backend",
					},
				},
				"namespace": "production",
			},
		},
	}

	result := convertCiliumServices(services)
	require.Len(t, result, 2)

	// First service by name
	require.NotNil(t, result[0].GetK8SService())
	assert.Equal(t, "my-service", result[0].GetK8SService().GetServiceName())
	assert.Equal(t, "default", result[0].GetK8SService().GetNamespace())

	// Second service by selector
	require.NotNil(t, result[1].GetK8SServiceSelector())
	assert.Equal(t, "backend", result[1].GetK8SServiceSelector().GetSelector().GetMatchLabels()["app"])
	assert.Equal(t, "production", result[1].GetK8SServiceSelector().GetNamespace())
}

func TestConvertCiliumLabelSelector_MatchExpressions(t *testing.T) {
	selector := map[string]any{
		"matchLabels": map[string]any{
			"app": "web",
		},
		"matchExpressions": []any{
			map[string]any{
				"key":      "environment",
				"operator": "In",
				"values":   []any{"prod", "staging"},
			},
			map[string]any{
				"key":      "deprecated",
				"operator": "DoesNotExist",
			},
		},
	}

	result := convertCiliumLabelSelector(selector)
	require.NotNil(t, result)

	// MatchLabels
	assert.Equal(t, "web", result.GetMatchLabels()["app"])

	// MatchExpressions
	require.Len(t, result.GetMatchExpressions(), 2)

	expr1 := result.GetMatchExpressions()[0]
	assert.Equal(t, "environment", expr1.GetKey())
	assert.Equal(t, "In", expr1.GetOperator())
	require.Len(t, expr1.GetValues(), 2)
	assert.Contains(t, expr1.GetValues(), "prod")
	assert.Contains(t, expr1.GetValues(), "staging")

	expr2 := result.GetMatchExpressions()[1]
	assert.Equal(t, "deprecated", expr2.GetKey())
	assert.Equal(t, "DoesNotExist", expr2.GetOperator())
}

func TestConvertCiliumDefaultDeny(t *testing.T) {
	defaultDeny := map[string]any{
		"ingress": true,
		"egress":  false,
	}

	result := convertCiliumDefaultDeny(defaultDeny)
	require.NotNil(t, result)
	require.NotNil(t, result.Ingress)
	assert.True(t, result.GetIngress())
	require.NotNil(t, result.Egress)
	assert.False(t, result.GetEgress())
}

func TestConvertCiliumPolicySpec_AllFields(t *testing.T) {
	spec := map[string]any{
		"endpointSelector": map[string]any{
			"matchLabels": map[string]any{"app": "web"},
		},
		"nodeSelector": map[string]any{
			"matchLabels": map[string]any{"node-type": "worker"},
		},
		"description": "Test policy",
		"labels": map[string]any{
			"policy-type": "security",
		},
		"enableDefaultDeny": map[string]any{
			"ingress": true,
		},
		"ingress": []any{
			map[string]any{
				"fromEntities": []any{"cluster"},
			},
		},
		"egress": []any{
			map[string]any{
				"toEntities": []any{"world"},
			},
		},
		"ingressDeny": []any{
			map[string]any{
				"fromEntities": []any{"world"},
			},
		},
		"egressDeny": []any{
			map[string]any{
				"toCIDR": []any{"10.0.0.0/8"},
			},
		},
	}

	result, err := convertCiliumPolicySpec(spec, "cilium.io/v2")
	require.NoError(t, err)
	require.NotNil(t, result)

	// EndpointSelector
	require.NotNil(t, result.GetEndpointSelector())
	assert.Equal(t, "web", result.GetEndpointSelector().GetMatchLabels()["app"])

	// NodeSelector
	require.NotNil(t, result.GetNodeSelector())
	assert.Equal(t, "worker", result.GetNodeSelector().GetMatchLabels()["node-type"])

	// Description
	require.NotNil(t, result.Description)
	assert.Equal(t, "Test policy", result.GetDescription())

	// Labels
	assert.Equal(t, "security", result.GetLabels()["policy-type"])

	// EnableDefaultDeny
	require.NotNil(t, result.GetEnableDefaultDeny())
	require.NotNil(t, result.GetEnableDefaultDeny().Ingress)
	assert.True(t, result.GetEnableDefaultDeny().GetIngress())

	// Ingress rules
	require.Len(t, result.GetIngressRules(), 1)
	assert.Contains(t, result.GetIngressRules()[0].GetFromEntities(), "cluster")

	// Egress rules
	require.Len(t, result.GetEgressRules(), 1)
	assert.Contains(t, result.GetEgressRules()[0].GetToEntities(), "world")

	// IngressDeny rules
	require.Len(t, result.GetIngressDenyRules(), 1)
	assert.Contains(t, result.GetIngressDenyRules()[0].GetFromEntities(), "world")

	// EgressDeny rules
	require.Len(t, result.GetEgressDenyRules(), 1)
	assert.Contains(t, result.GetEgressDenyRules()[0].GetToCidr(), "10.0.0.0/8")
}

// Tests for nil/empty handling

func TestConvertCiliumLabelSelector_Nil(t *testing.T) {
	result := convertCiliumLabelSelector(nil)
	assert.Nil(t, result)
}

func TestConvertCiliumLabelSelector_Empty(t *testing.T) {
	result := convertCiliumLabelSelector(map[string]any{})
	require.NotNil(t, result)
	assert.Empty(t, result.GetMatchLabels())
	assert.Empty(t, result.GetMatchExpressions())
}

func TestConvertCiliumEndpointSelectors(t *testing.T) {
	selectors := []any{
		map[string]any{
			"matchLabels": map[string]any{"app": "frontend"},
		},
		map[string]any{
			"matchLabels": map[string]any{"app": "backend"},
		},
	}

	result := convertCiliumEndpointSelectors(selectors)
	require.Len(t, result, 2)
	assert.Equal(t, "frontend", result[0].GetMatchLabels()["app"])
	assert.Equal(t, "backend", result[1].GetMatchLabels()["app"])
}

func TestConvertCiliumEndpointSelectors_Empty(t *testing.T) {
	result := convertCiliumEndpointSelectors([]any{})
	assert.Nil(t, result)
}

func TestConvertCiliumEndpointSelectors_InvalidType(t *testing.T) {
	// Contains an invalid type that should be skipped
	selectors := []any{
		"not-a-map",
		map[string]any{"matchLabels": map[string]any{"app": "valid"}},
	}

	result := convertCiliumEndpointSelectors(selectors)
	require.Len(t, result, 1)
	assert.Equal(t, "valid", result[0].GetMatchLabels()["app"])
}

func TestConvertCiliumFQDNSelectors(t *testing.T) {
	fqdns := []any{
		map[string]any{"matchName": "api.example.com"},
		map[string]any{"matchPattern": "*.internal.com"},
	}

	result := convertCiliumFQDNSelectors(fqdns)
	require.Len(t, result, 2)
	assert.Equal(t, "api.example.com", result[0].GetMatchName())
	assert.Equal(t, "*.internal.com", result[1].GetMatchPattern())
}

func TestConvertCiliumFQDNSelectors_Empty(t *testing.T) {
	result := convertCiliumFQDNSelectors([]any{})
	assert.Nil(t, result)
}

func TestConvertCiliumFQDNSelectors_NoMatchFields(t *testing.T) {
	// FQDN with neither matchName nor matchPattern
	fqdns := []any{
		map[string]any{"other": "value"},
	}

	result := convertCiliumFQDNSelectors(fqdns)
	require.Len(t, result, 1)
	// Should have neither matchName nor matchPattern set
	assert.Empty(t, result[0].GetMatchName())
	assert.Empty(t, result[0].GetMatchPattern())
}

func TestConvertCiliumPortRules(t *testing.T) {
	portRules := []any{
		map[string]any{
			"ports": []any{
				map[string]any{"port": "80", "protocol": "TCP"},
				map[string]any{"port": "443", "protocol": "TCP"},
			},
		},
	}

	result := convertCiliumPortRules(portRules)
	require.Len(t, result, 1)
	require.Len(t, result[0].GetPorts(), 2)
	assert.Equal(t, "80", result[0].GetPorts()[0].GetPort())
	assert.Equal(t, "443", result[0].GetPorts()[1].GetPort())
}

func TestConvertCiliumPortRules_Empty(t *testing.T) {
	result := convertCiliumPortRules([]any{})
	assert.Nil(t, result)
}

func TestConvertCiliumGroups_Empty(t *testing.T) {
	result := convertCiliumGroups([]any{})
	assert.Nil(t, result)
}

func TestConvertCiliumGroups_NoCloudProvider(t *testing.T) {
	// Group without any cloud provider config
	groups := []any{
		map[string]any{
			"unknown": map[string]any{"field": "value"},
		},
	}

	result := convertCiliumGroups(groups)
	require.Len(t, result, 1)
	// Should have nil cloud provider
	assert.Nil(t, result[0].GetAws())
}

func TestConvertCiliumIngressRules_AllFields(t *testing.T) {
	rules := []any{
		map[string]any{
			"fromEndpoints": []any{
				map[string]any{"matchLabels": map[string]any{"app": "backend"}},
			},
			"fromCIDR": []any{"10.0.0.0/8"},
			"fromCIDRSet": []any{
				map[string]any{"cidr": "192.168.0.0/16", "except": []any{"192.168.1.0/24"}},
			},
			"fromEntities": []any{"cluster"},
			"fromGroups": []any{
				map[string]any{
					"aws": map[string]any{"securityGroupsIds": []any{"sg-123"}},
				},
			},
			"fromNodes": []any{
				map[string]any{"matchLabels": map[string]any{"node-role": "worker"}},
			},
			"toPorts": []any{
				map[string]any{"ports": []any{map[string]any{"port": "80", "protocol": "TCP"}}},
			},
			"icmps": []any{
				map[string]any{"fields": []any{map[string]any{"type": int64(8)}}},
			},
			"authentication": map[string]any{"mode": "required"},
		},
	}

	result, err := convertCiliumIngressRules(rules)
	require.NoError(t, err)
	require.Len(t, result, 1)

	rule := result[0]

	// FromEndpoints
	require.NotNil(t, rule.GetFromEndpoints())
	require.Len(t, rule.GetFromEndpoints().GetItems(), 1)
	assert.Equal(t, "backend", rule.GetFromEndpoints().GetItems()[0].GetMatchLabels()["app"])

	// FromCIDR
	require.Len(t, rule.GetFromCidr(), 1)
	assert.Equal(t, "10.0.0.0/8", rule.GetFromCidr()[0])

	// FromCIDRSet
	require.Len(t, rule.GetFromCidrSet(), 1)
	assert.Equal(t, "192.168.0.0/16", rule.GetFromCidrSet()[0].GetCidr())

	// FromEntities
	require.Len(t, rule.GetFromEntities(), 1)
	assert.Equal(t, "cluster", rule.GetFromEntities()[0])

	// FromGroups
	require.Len(t, rule.GetFromGroups(), 1)
	require.NotNil(t, rule.GetFromGroups()[0].GetAws())

	// FromNodes
	require.Len(t, rule.GetFromNodes(), 1)
	assert.Equal(t, "worker", rule.GetFromNodes()[0].GetMatchLabels()["node-role"])

	// ToPorts
	require.Len(t, rule.GetToPorts(), 1)

	// ICMPs
	require.Len(t, rule.GetIcmps(), 1)

	// Authentication
	require.NotNil(t, rule.GetAuthentication())
	assert.Equal(t, "required", rule.GetAuthentication().GetMode())
}

func TestConvertCiliumIngressRules_Empty(t *testing.T) {
	result, err := convertCiliumIngressRules([]any{})
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestConvertCiliumIngressRules_InvalidRuleType(t *testing.T) {
	rules := []any{
		"not-a-map",
	}

	result, err := convertCiliumIngressRules(rules)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected map")
	assert.Nil(t, result)
}

func TestConvertCiliumEgressRules_AllFields(t *testing.T) {
	rules := []any{
		map[string]any{
			"toEndpoints": []any{
				map[string]any{"matchLabels": map[string]any{"app": "database"}},
			},
			"toCIDR": []any{"0.0.0.0/0"},
			"toCIDRSet": []any{
				map[string]any{"cidr": "10.0.0.0/8"},
			},
			"toEntities": []any{"world"},
			"toFQDNs": []any{
				map[string]any{"matchName": "api.example.com"},
			},
			"toServices": []any{
				map[string]any{
					"k8sService": map[string]any{"serviceName": "my-svc", "namespace": "default"},
				},
			},
			"toGroups": []any{
				map[string]any{
					"aws": map[string]any{"securityGroupsIds": []any{"sg-456"}},
				},
			},
			"toNodes": []any{
				map[string]any{"matchLabels": map[string]any{"node-type": "egress"}},
			},
			"toPorts": []any{
				map[string]any{"ports": []any{map[string]any{"port": "443", "protocol": "TCP"}}},
			},
			"icmps": []any{
				map[string]any{"fields": []any{map[string]any{"type": int64(0)}}},
			},
			"authentication": map[string]any{"mode": "disabled"},
		},
	}

	result, err := convertCiliumEgressRules(rules)
	require.NoError(t, err)
	require.Len(t, result, 1)

	rule := result[0]

	// ToEndpoints
	require.NotNil(t, rule.GetToEndpoints())
	require.Len(t, rule.GetToEndpoints().GetItems(), 1)
	assert.Equal(t, "database", rule.GetToEndpoints().GetItems()[0].GetMatchLabels()["app"])

	// ToCIDR
	require.Len(t, rule.GetToCidr(), 1)
	assert.Equal(t, "0.0.0.0/0", rule.GetToCidr()[0])

	// ToCIDRSet
	require.Len(t, rule.GetToCidrSet(), 1)
	assert.Equal(t, "10.0.0.0/8", rule.GetToCidrSet()[0].GetCidr())

	// ToEntities
	require.Len(t, rule.GetToEntities(), 1)
	assert.Equal(t, "world", rule.GetToEntities()[0])

	// ToFQDNs
	require.Len(t, rule.GetToFqdns(), 1)
	assert.Equal(t, "api.example.com", rule.GetToFqdns()[0].GetMatchName())

	// ToServices
	require.Len(t, rule.GetToServices(), 1)
	require.NotNil(t, rule.GetToServices()[0].GetK8SService())
	assert.Equal(t, "my-svc", rule.GetToServices()[0].GetK8SService().GetServiceName())

	// ToGroups
	require.Len(t, rule.GetToGroups(), 1)
	require.NotNil(t, rule.GetToGroups()[0].GetAws())

	// ToNodes
	require.Len(t, rule.GetToNodes(), 1)
	assert.Equal(t, "egress", rule.GetToNodes()[0].GetMatchLabels()["node-type"])

	// ToPorts
	require.Len(t, rule.GetToPorts(), 1)

	// ICMPs
	require.Len(t, rule.GetIcmps(), 1)

	// Authentication
	require.NotNil(t, rule.GetAuthentication())
	assert.Equal(t, "disabled", rule.GetAuthentication().GetMode())
}

func TestConvertCiliumEgressRules_Empty(t *testing.T) {
	result, err := convertCiliumEgressRules([]any{})
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestConvertCiliumEgressRules_InvalidRuleType(t *testing.T) {
	rules := []any{
		"not-a-map",
	}

	result, err := convertCiliumEgressRules(rules)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected map")
	assert.Nil(t, result)
}

func TestConvertCiliumICMPFields_Empty(t *testing.T) {
	result := convertCiliumICMPFields([]any{})
	assert.Nil(t, result)
}

func TestConvertCiliumICMPRules_Empty(t *testing.T) {
	result := convertCiliumICMPRules([]any{})
	assert.Nil(t, result)
}

func TestConvertCiliumCIDRSets_Empty(t *testing.T) {
	result := convertCiliumCIDRSets([]any{})
	assert.Nil(t, result)
}

func TestConvertCiliumServices_Empty(t *testing.T) {
	result := convertCiliumServices([]any{})
	assert.Nil(t, result)
}

func TestConvertCiliumPorts_Empty(t *testing.T) {
	result := convertCiliumPorts([]any{})
	assert.Nil(t, result)
}

func TestConvertUnstructuredToCiliumPolicy_WithOwnerReferences(t *testing.T) {
	obj := newTestCiliumNetworkPolicy("test-policy", "default", map[string]any{
		"endpointSelector": map[string]any{},
	})

	// Add owner references
	obj.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Name:       "my-deployment",
			UID:        "owner-uid-123",
		},
	})

	result, err := ConvertUnstructuredToCiliumPolicy(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Len(t, result.GetOwnerReferences(), 1)
	assert.Equal(t, "apps/v1", result.GetOwnerReferences()[0].GetApiVersion())
	assert.Equal(t, "Deployment", result.GetOwnerReferences()[0].GetKind())
	assert.Equal(t, "my-deployment", result.GetOwnerReferences()[0].GetName())
	assert.Equal(t, "owner-uid-123", result.GetOwnerReferences()[0].GetUid())
}

func TestConvertUnstructuredToCiliumPolicy_WithLabelsAndAnnotations(t *testing.T) {
	obj := newTestCiliumNetworkPolicy("test-policy", "default", map[string]any{
		"endpointSelector": map[string]any{},
	})

	obj.SetLabels(map[string]string{
		"app":     "myapp",
		"version": "v1",
	})
	obj.SetAnnotations(map[string]string{
		"description": "Test policy annotation",
	})

	result, err := ConvertUnstructuredToCiliumPolicy(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "myapp", result.GetLabels()["app"])
	assert.Equal(t, "v1", result.GetLabels()["version"])
	assert.Equal(t, "Test policy annotation", result.GetAnnotations()["description"])
}

func TestConvertCiliumMatchExpressions_Empty(t *testing.T) {
	result := convertCiliumMatchExpressions([]any{})
	assert.Nil(t, result)
}

func TestConvertCiliumMatchExpressions_InvalidType(t *testing.T) {
	expressions := []any{
		"not-a-map",
		map[string]any{"key": "valid", "operator": "Exists"},
	}

	result := convertCiliumMatchExpressions(expressions)
	require.Len(t, result, 1)
	assert.Equal(t, "valid", result[0].GetKey())
}

// =============================================================================
// USE CASE TESTS
// These tests mirror actual Cilium policy patterns from POC use cases.
// See: /Documents/sandbox/poc/agentless_segmentation/poc/use-cases/
// =============================================================================

// TestUseCase_P2UC3_APIServerIsolation mirrors the API Server Isolation use case.
// Policy denies all pods (except those with api-access=true) from reaching kube-apiserver.
func TestUseCase_P2UC3_APIServerIsolation(t *testing.T) {
	// This mirrors p2-uc3-api-server-isolation/manifests/policies.yaml
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("cilium.io/v2")
	obj.SetKind("CiliumClusterwideNetworkPolicy")
	obj.SetName("deny-api-server-default")
	obj.SetUID("test-uid")
	obj.SetResourceVersion("12345")
	obj.SetCreationTimestamp(metav1.Now())

	obj.Object["spec"] = map[string]any{
		"description": "Default deny all pods from reaching Kubernetes API server",
		"endpointSelector": map[string]any{
			// Select pods that do NOT have api-access=true label
			"matchExpressions": []any{
				map[string]any{
					"key":      "api-access",
					"operator": "NotIn",
					"values":   []any{"true"},
				},
			},
		},
		"egressDeny": []any{
			// Deny to kube-apiserver entity
			map[string]any{
				"toEntities": []any{"kube-apiserver"},
			},
			// Also deny to kubernetes service
			map[string]any{
				"toServices": []any{
					map[string]any{
						"k8sService": map[string]any{
							"serviceName": "kubernetes",
							"namespace":   "default",
						},
					},
				},
			},
		},
	}

	result, err := ConvertUnstructuredToCiliumPolicy(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "CiliumClusterwideNetworkPolicy", result.GetKind())
	assert.Equal(t, "deny-api-server-default", result.GetName())

	ccnp := result.GetCiliumClusterwideNetworkPolicy()
	require.NotNil(t, ccnp)
	require.Len(t, ccnp.GetSpecs(), 1)

	spec := ccnp.GetSpecs()[0]

	// Verify description
	require.NotNil(t, spec.Description)
	assert.Contains(t, spec.GetDescription(), "deny all pods")

	// Verify matchExpressions with NotIn operator
	require.NotNil(t, spec.GetEndpointSelector())
	require.Len(t, spec.GetEndpointSelector().GetMatchExpressions(), 1)
	expr := spec.GetEndpointSelector().GetMatchExpressions()[0]
	assert.Equal(t, "api-access", expr.GetKey())
	assert.Equal(t, "NotIn", expr.GetOperator())
	assert.Contains(t, expr.GetValues(), "true")

	// Verify egressDeny rules
	require.Len(t, spec.GetEgressDenyRules(), 2)

	// First deny rule: toEntities kube-apiserver
	assert.Contains(t, spec.GetEgressDenyRules()[0].GetToEntities(), "kube-apiserver")

	// Second deny rule: toServices kubernetes
	require.Len(t, spec.GetEgressDenyRules()[1].GetToServices(), 1)
	require.NotNil(t, spec.GetEgressDenyRules()[1].GetToServices()[0].GetK8SService())
	assert.Equal(t, "kubernetes", spec.GetEgressDenyRules()[1].GetToServices()[0].GetK8SService().GetServiceName())
	assert.Equal(t, "default", spec.GetEgressDenyRules()[1].GetToServices()[0].GetK8SService().GetNamespace())
}

// TestUseCase_P2UC3_AllowClusterInternal mirrors the allow-cluster-internal policy.
// Allows all pods to communicate within cluster and reach external (world, host).
func TestUseCase_P2UC3_AllowClusterInternal(t *testing.T) {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("cilium.io/v2")
	obj.SetKind("CiliumClusterwideNetworkPolicy")
	obj.SetName("allow-cluster-internal")
	obj.SetUID("test-uid")
	obj.SetResourceVersion("12345")
	obj.SetCreationTimestamp(metav1.Now())

	obj.Object["spec"] = map[string]any{
		"description": "Allow all pods to communicate within the cluster",
		// Empty selector matches all pods
		"endpointSelector": map[string]any{},
		"egress": []any{
			// Allow pod-to-pod (empty toEndpoints)
			map[string]any{
				"toEndpoints": []any{
					map[string]any{}, // Empty selector = all endpoints
				},
			},
			// Allow to host
			map[string]any{
				"toEntities": []any{"host"},
				"toPorts": []any{
					map[string]any{
						"ports": []any{
							map[string]any{"port": "10250", "protocol": "TCP"},
						},
					},
				},
			},
			// Allow internet egress
			map[string]any{
				"toEntities": []any{"world"},
			},
		},
	}

	result, err := ConvertUnstructuredToCiliumPolicy(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ccnp := result.GetCiliumClusterwideNetworkPolicy()
	require.NotNil(t, ccnp)
	require.Len(t, ccnp.GetSpecs(), 1)

	spec := ccnp.GetSpecs()[0]

	// Verify empty endpointSelector (selects all pods)
	require.NotNil(t, spec.GetEndpointSelector())
	assert.Empty(t, spec.GetEndpointSelector().GetMatchLabels())
	assert.Empty(t, spec.GetEndpointSelector().GetMatchExpressions())

	// Verify egress rules
	require.Len(t, spec.GetEgressRules(), 3)

	// Rule 1: toEndpoints with empty selector
	require.NotNil(t, spec.GetEgressRules()[0].GetToEndpoints())
	require.Len(t, spec.GetEgressRules()[0].GetToEndpoints().GetItems(), 1)
	assert.Empty(t, spec.GetEgressRules()[0].GetToEndpoints().GetItems()[0].GetMatchLabels())

	// Rule 2: toEntities host
	assert.Contains(t, spec.GetEgressRules()[1].GetToEntities(), "host")
	require.Len(t, spec.GetEgressRules()[1].GetToPorts(), 1)

	// Rule 3: toEntities world
	assert.Contains(t, spec.GetEgressRules()[2].GetToEntities(), "world")
}

// TestUseCase_P3UC31_CIDRWithExclusions mirrors UC3.1 scenario 5.
// Uses toCIDRSet with inline CIDRs and exclusions.
func TestUseCase_P3UC31_CIDRWithExclusions(t *testing.T) {
	// This mirrors UC3.1-pod-to-cloud-resource-cidr/manifests/scenario-5-tocidrset-exclusions
	obj := newTestCiliumClusterwideNetworkPolicy("data-processor-aws-with-exclusions", map[string]any{
		"description": "data-processor → AWS (toCIDRSet with DynamoDB excluded)",
		"endpointSelector": map[string]any{
			"matchLabels": map[string]any{
				"k8s:io.kubernetes.pod.namespace": "data-ns",
				"k8s:role":                        "data-processor",
			},
		},
		"egress": []any{
			map[string]any{
				// toCIDRSet with exclusions
				"toCIDRSet": []any{
					map[string]any{
						"cidr": "52.0.0.0/8", // Broad AWS range
						"except": []any{
							"52.94.0.0/22",    // EXCLUDE: DynamoDB us-east-1
							"52.119.224.0/20", // EXCLUDE: DynamoDB us-west-2
							"52.95.0.0/20",    // EXCLUDE: Internal AWS services
						},
					},
					map[string]any{
						"cidr": "54.231.0.0/16", // Additional S3 range (no exclusions)
					},
				},
				"toPorts": []any{
					map[string]any{
						"ports": []any{
							map[string]any{"port": "443", "protocol": "TCP"},
						},
					},
				},
			},
		},
	})

	result, err := ConvertUnstructuredToCiliumPolicy(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ccnp := result.GetCiliumClusterwideNetworkPolicy()
	require.NotNil(t, ccnp)
	require.Len(t, ccnp.GetSpecs(), 1)

	spec := ccnp.GetSpecs()[0]

	// Verify egress rule with toCIDRSet
	require.Len(t, spec.GetEgressRules(), 1)
	rule := spec.GetEgressRules()[0]

	// Verify toCIDRSet with multiple CIDRs and exclusions
	require.Len(t, rule.GetToCidrSet(), 2)

	// First CIDR with exclusions
	assert.Equal(t, "52.0.0.0/8", rule.GetToCidrSet()[0].GetCidr())
	require.Len(t, rule.GetToCidrSet()[0].GetExcept(), 3)
	assert.Contains(t, rule.GetToCidrSet()[0].GetExcept(), "52.94.0.0/22")
	assert.Contains(t, rule.GetToCidrSet()[0].GetExcept(), "52.119.224.0/20")
	assert.Contains(t, rule.GetToCidrSet()[0].GetExcept(), "52.95.0.0/20")

	// Second CIDR without exclusions
	assert.Equal(t, "54.231.0.0/16", rule.GetToCidrSet()[1].GetCidr())
	assert.Empty(t, rule.GetToCidrSet()[1].GetExcept())

	// Verify toPorts
	require.Len(t, rule.GetToPorts(), 1)
	require.Len(t, rule.GetToPorts()[0].GetPorts(), 1)
	assert.Equal(t, "443", rule.GetToPorts()[0].GetPorts()[0].GetPort())
}

// TestUseCase_P3UC3_FQDNEgress mirrors UC3 FQDN-based egress.
// Uses toFQDNs with wildcards for cloud services.
func TestUseCase_P3UC3_FQDNEgress(t *testing.T) {
	// This mirrors UC3-pod-to-cloud-resource/manifests/policies.yaml
	obj := newTestCiliumClusterwideNetworkPolicy("data-processor-egress", map[string]any{
		"description": "data-processor: Allow S3 + DynamoDB",
		"endpointSelector": map[string]any{
			"matchLabels": map[string]any{
				"k8s:io.kubernetes.pod.namespace": "data-ns",
				"k8s:role":                        "data-processor",
			},
		},
		"egress": []any{
			// Allow AWS S3 with wildcards
			map[string]any{
				"toFQDNs": []any{
					map[string]any{"matchPattern": "*.s3.amazonaws.com"},
					map[string]any{"matchPattern": "s3.amazonaws.com"},
					map[string]any{"matchPattern": "*.s3.*.amazonaws.com"},
					map[string]any{"matchPattern": "s3.*.amazonaws.com"},
				},
				"toPorts": []any{
					map[string]any{
						"ports": []any{
							map[string]any{"port": "443", "protocol": "TCP"},
						},
					},
				},
			},
			// Allow AWS DynamoDB
			map[string]any{
				"toFQDNs": []any{
					map[string]any{"matchPattern": "dynamodb.*.amazonaws.com"},
				},
				"toPorts": []any{
					map[string]any{
						"ports": []any{
							map[string]any{"port": "443", "protocol": "TCP"},
						},
					},
				},
			},
		},
	})

	result, err := ConvertUnstructuredToCiliumPolicy(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ccnp := result.GetCiliumClusterwideNetworkPolicy()
	require.NotNil(t, ccnp)
	require.Len(t, ccnp.GetSpecs(), 1)

	spec := ccnp.GetSpecs()[0]

	// Verify egress rules with toFQDNs
	require.Len(t, spec.GetEgressRules(), 2)

	// First rule: S3 with multiple wildcard patterns
	s3Rule := spec.GetEgressRules()[0]
	require.Len(t, s3Rule.GetToFqdns(), 4)
	assert.Equal(t, "*.s3.amazonaws.com", s3Rule.GetToFqdns()[0].GetMatchPattern())
	assert.Equal(t, "s3.amazonaws.com", s3Rule.GetToFqdns()[1].GetMatchPattern())
	assert.Equal(t, "*.s3.*.amazonaws.com", s3Rule.GetToFqdns()[2].GetMatchPattern())
	assert.Equal(t, "s3.*.amazonaws.com", s3Rule.GetToFqdns()[3].GetMatchPattern())

	// Second rule: DynamoDB
	dynamoRule := spec.GetEgressRules()[1]
	require.Len(t, dynamoRule.GetToFqdns(), 1)
	assert.Equal(t, "dynamodb.*.amazonaws.com", dynamoRule.GetToFqdns()[0].GetMatchPattern())
}

// TestUseCase_P3UC3_IngressFromLabels mirrors intra-namespace access control.
// Uses ingress with fromEndpoints and label selectors.
func TestUseCase_P3UC3_IngressFromLabels(t *testing.T) {
	// This mirrors the data-cache-ingress policy
	obj := newTestCiliumClusterwideNetworkPolicy("data-cache-ingress", map[string]any{
		"description": "data-cache: Allow from data-processor and report-gen only",
		"endpointSelector": map[string]any{
			"matchLabels": map[string]any{
				"k8s:io.kubernetes.pod.namespace": "data-ns",
				"k8s:app":                         "data-cache",
			},
		},
		"ingress": []any{
			// Allow from data-processor
			map[string]any{
				"fromEndpoints": []any{
					map[string]any{
						"matchLabels": map[string]any{
							"k8s:io.kubernetes.pod.namespace": "data-ns",
							"k8s:role":                        "data-processor",
						},
					},
				},
				"toPorts": []any{
					map[string]any{
						"ports": []any{
							map[string]any{"port": "80", "protocol": "TCP"},
						},
					},
				},
			},
			// Allow from report-gen
			map[string]any{
				"fromEndpoints": []any{
					map[string]any{
						"matchLabels": map[string]any{
							"k8s:io.kubernetes.pod.namespace": "data-ns",
							"k8s:role":                        "report-gen",
						},
					},
				},
				"toPorts": []any{
					map[string]any{
						"ports": []any{
							map[string]any{"port": "80", "protocol": "TCP"},
						},
					},
				},
			},
		},
	})

	result, err := ConvertUnstructuredToCiliumPolicy(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	ccnp := result.GetCiliumClusterwideNetworkPolicy()
	require.NotNil(t, ccnp)
	require.Len(t, ccnp.GetSpecs(), 1)

	spec := ccnp.GetSpecs()[0]

	// Verify endpoint selector targets data-cache
	require.NotNil(t, spec.GetEndpointSelector())
	assert.Equal(t, "data-cache", spec.GetEndpointSelector().GetMatchLabels()["k8s:app"])

	// Verify ingress rules
	require.Len(t, spec.GetIngressRules(), 2)

	// First rule: from data-processor
	require.NotNil(t, spec.GetIngressRules()[0].GetFromEndpoints())
	require.Len(t, spec.GetIngressRules()[0].GetFromEndpoints().GetItems(), 1)
	assert.Equal(t, "data-processor", spec.GetIngressRules()[0].GetFromEndpoints().GetItems()[0].GetMatchLabels()["k8s:role"])

	// Second rule: from report-gen
	require.NotNil(t, spec.GetIngressRules()[1].GetFromEndpoints())
	require.Len(t, spec.GetIngressRules()[1].GetFromEndpoints().GetItems(), 1)
	assert.Equal(t, "report-gen", spec.GetIngressRules()[1].GetFromEndpoints().GetItems()[0].GetMatchLabels()["k8s:role"])
}

// Helper functions to create test objects

func newTestCiliumNetworkPolicy(name, namespace string, spec map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("cilium.io/v2")
	obj.SetKind("CiliumNetworkPolicy")
	obj.SetName(name)
	obj.SetNamespace(namespace)
	obj.SetUID("test-uid")
	obj.SetResourceVersion("12345")
	obj.SetCreationTimestamp(metav1.Now())

	if spec != nil {
		obj.Object["spec"] = spec
	}

	return obj
}

func newTestCiliumClusterwideNetworkPolicy(name string, spec map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("cilium.io/v2")
	obj.SetKind("CiliumClusterwideNetworkPolicy")
	obj.SetName(name)
	obj.SetUID("test-uid")
	obj.SetResourceVersion("12345")
	obj.SetCreationTimestamp(metav1.Now())

	if spec != nil {
		obj.Object["spec"] = spec
	}

	return obj
}
