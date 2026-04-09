// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
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
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot convert nil object")
}

func TestConvertUnstructuredToCiliumPolicy_UnsupportedKind(t *testing.T) {
	obj := &unstructured.Unstructured{}
	obj.SetKind("UnsupportedKind")
	obj.SetName("test-policy")
	obj.SetNamespace("default")

	result, err := ConvertUnstructuredToCiliumPolicy(obj)
	assert.Nil(t, result)
	assert.Error(t, err)
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

	assert.Equal(t, "CiliumNetworkPolicy", result.Kind)
	assert.Equal(t, "test-policy", result.Name)
	assert.Equal(t, "default", result.Namespace)

	// Verify kind_specific is set correctly
	cnp := result.GetCiliumNetworkPolicy()
	require.NotNil(t, cnp)
	require.Len(t, cnp.Specs, 1)

	// Verify endpoint selector
	spec := cnp.Specs[0]
	require.NotNil(t, spec.EndpointSelector)
	assert.Equal(t, "web", spec.EndpointSelector.MatchLabels["app"])

	// Verify ingress rules
	require.Len(t, spec.IngressRules, 1)
	ingressRule := spec.IngressRules[0]
	require.Len(t, ingressRule.FromEndpoints, 1)
	assert.Equal(t, "backend", ingressRule.FromEndpoints[0].MatchLabels["app"])

	// Verify ports
	require.Len(t, ingressRule.ToPorts, 1)
	require.Len(t, ingressRule.ToPorts[0].Ports, 1)
	assert.Equal(t, "80", ingressRule.ToPorts[0].Ports[0].Port)
	assert.Equal(t, pb.CiliumProtocol_CILIUM_PROTOCOL_TCP, ingressRule.ToPorts[0].Ports[0].Protocol)
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

	assert.Equal(t, "CiliumClusterwideNetworkPolicy", result.Kind)
	assert.Equal(t, "test-ccnp", result.Name)
	assert.Empty(t, result.Namespace) // Cluster-scoped

	// Verify kind_specific is set correctly
	ccnp := result.GetCiliumClusterwideNetworkPolicy()
	require.NotNil(t, ccnp)
	require.Len(t, ccnp.Specs, 1)

	// Verify node selector
	spec := ccnp.Specs[0]
	require.NotNil(t, spec.NodeSelector)
	assert.Equal(t, "worker", spec.NodeSelector.MatchLabels["node-type"])
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
	assert.Equal(t, "test", specs[0].EndpointSelector.MatchLabels["app"])
	require.NotNil(t, specs[0].ApiVersion)
	assert.Equal(t, "cilium.io/v2", *specs[0].ApiVersion)
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
	assert.Equal(t, "spec1", specs[0].EndpointSelector.MatchLabels["app"])
	assert.Equal(t, "spec2", specs[1].EndpointSelector.MatchLabels["app"])
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
	assert.Equal(t, "from-spec", specs[0].EndpointSelector.MatchLabels["app"])
	assert.Equal(t, "from-specs", specs[1].EndpointSelector.MatchLabels["app"])
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
	assert.Equal(t, "80", result[0].Port)
	assert.Equal(t, pb.CiliumProtocol_CILIUM_PROTOCOL_TCP, result[0].Protocol)
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
	assert.Equal(t, "http", result[0].Port)
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
	assert.Equal(t, "8080", result[0].Port)
	require.NotNil(t, result[0].EndPort)
	assert.Equal(t, int32(8090), *result[0].EndPort)
}

func TestParseCiliumProtocol(t *testing.T) {
	tests := []struct {
		input    string
		expected pb.CiliumProtocol
	}{
		{"TCP", pb.CiliumProtocol_CILIUM_PROTOCOL_TCP},
		{"UDP", pb.CiliumProtocol_CILIUM_PROTOCOL_UDP},
		{"SCTP", pb.CiliumProtocol_CILIUM_PROTOCOL_SCTP},
		{"ICMP", pb.CiliumProtocol_CILIUM_PROTOCOL_ICMP},
		{"ICMPV6", pb.CiliumProtocol_CILIUM_PROTOCOL_ICMPV6},
		{"ICMPv6", pb.CiliumProtocol_CILIUM_PROTOCOL_ICMPV6}, // alternate casing
		{"ANY", pb.CiliumProtocol_CILIUM_PROTOCOL_ANY},
		{"", pb.CiliumProtocol_CILIUM_PROTOCOL_UNSPECIFIED},
		{"unknown", pb.CiliumProtocol_CILIUM_PROTOCOL_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseCiliumProtocol(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertCiliumGroups_AWSSecurityGroups(t *testing.T) {
	groups := []any{
		map[string]any{
			"aws": map[string]any{
				"region":            "us-west-2",
				"securityGroupsIds": []any{"sg-123", "sg-456"},
				"vpcId":             "vpc-abc",
			},
		},
	}

	result := convertCiliumGroups(groups)
	require.Len(t, result, 1)

	// Access AWS fields via oneof accessor
	aws := result[0].GetAws()
	require.NotNil(t, aws, "expected AWS cloud provider")

	require.NotNil(t, aws.Region)
	assert.Equal(t, "us-west-2", *aws.Region)

	require.Len(t, aws.SecurityGroupIds, 2)
	assert.Equal(t, "sg-123", aws.SecurityGroupIds[0])
	assert.Equal(t, "sg-456", aws.SecurityGroupIds[1])

	require.NotNil(t, aws.VpcId)
	assert.Equal(t, "vpc-abc", *aws.VpcId)
}

func TestConvertCiliumIngressRules(t *testing.T) {
	rules := []any{
		map[string]any{
			"fromEndpoints": []any{
				map[string]any{
					"matchLabels": map[string]any{"app": "backend"},
				},
			},
			"fromCIDR": []any{"10.0.0.0/8"},
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
	require.Len(t, rule.FromEndpoints, 1)
	assert.Equal(t, "backend", rule.FromEndpoints[0].MatchLabels["app"])

	// FromCIDR
	require.Len(t, rule.FromCidr, 1)
	assert.Equal(t, "10.0.0.0/8", rule.FromCidr[0])

	// FromEntities
	require.Len(t, rule.FromEntities, 2)
	assert.Contains(t, rule.FromEntities, "world")
	assert.Contains(t, rule.FromEntities, "cluster")

	// ToPorts
	require.Len(t, rule.ToPorts, 1)
	require.Len(t, rule.ToPorts[0].Ports, 1)
	assert.Equal(t, "443", rule.ToPorts[0].Ports[0].Port)
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
	require.Len(t, rule.ToEndpoints, 1)
	assert.Equal(t, "database", rule.ToEndpoints[0].MatchLabels["app"])

	// ToCIDR
	require.Len(t, rule.ToCidr, 1)
	assert.Equal(t, "0.0.0.0/0", rule.ToCidr[0])

	// ToFQDNs (oneof: either MatchName or MatchPattern)
	require.Len(t, rule.ToFqdns, 2)
	assert.Equal(t, "api.example.com", rule.ToFqdns[0].GetMatchName())
	assert.Equal(t, "*.internal.com", rule.ToFqdns[1].GetMatchPattern())

	// ToEntities
	require.Len(t, rule.ToEntities, 1)
	assert.Equal(t, "world", rule.ToEntities[0])
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
	assert.Equal(t, "10.0.0.0/8", result[0].Cidr)
	require.Len(t, result[0].Except, 2)
	assert.Contains(t, result[0].Except, "10.1.0.0/16")
	assert.Contains(t, result[0].Except, "10.2.0.0/16")

	// Second CIDR set with group reference
	require.NotNil(t, result[1].CidrGroupRef)
	assert.Equal(t, "my-cidr-group", *result[1].CidrGroupRef)
}

func TestConvertCiliumICMPRules(t *testing.T) {
	icmps := []any{
		map[string]any{
			"fields": []any{
				map[string]any{
					"type":   int64(8),
					"code":   int64(0),
					"family": "IPv4",
				},
			},
		},
	}

	result := convertCiliumICMPRules(icmps)
	require.Len(t, result, 1)
	require.Len(t, result[0].Fields, 1)

	field := result[0].Fields[0]
	assert.Equal(t, uint32(8), field.Type)
	require.NotNil(t, field.Code)
	assert.Equal(t, uint32(0), *field.Code)
	require.NotNil(t, field.Family)
	assert.Equal(t, "IPv4", *field.Family)
}

func TestConvertCiliumAuthentication(t *testing.T) {
	auth := map[string]any{
		"mode": "required",
	}

	result := convertCiliumAuthentication(auth)
	require.NotNil(t, result)
	assert.Equal(t, "required", result.Mode)
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
	require.NotNil(t, result[0].K8SService)
	assert.Equal(t, "my-service", *result[0].K8SService)
	require.NotNil(t, result[0].K8SNamespace)
	assert.Equal(t, "default", *result[0].K8SNamespace)

	// Second service by selector
	require.NotNil(t, result[1].K8SServiceSelector)
	assert.Equal(t, "backend", result[1].K8SServiceSelector.MatchLabels["app"])
	require.NotNil(t, result[1].K8SNamespace)
	assert.Equal(t, "production", *result[1].K8SNamespace)
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
	assert.Equal(t, "web", result.MatchLabels["app"])

	// MatchExpressions
	require.Len(t, result.MatchExpressions, 2)

	expr1 := result.MatchExpressions[0]
	assert.Equal(t, "environment", expr1.Key)
	assert.Equal(t, "In", expr1.Operator)
	require.Len(t, expr1.Values, 2)
	assert.Contains(t, expr1.Values, "prod")
	assert.Contains(t, expr1.Values, "staging")

	expr2 := result.MatchExpressions[1]
	assert.Equal(t, "deprecated", expr2.Key)
	assert.Equal(t, "DoesNotExist", expr2.Operator)
}

func TestConvertCiliumDefaultDeny(t *testing.T) {
	defaultDeny := map[string]any{
		"ingress": true,
		"egress":  false,
	}

	result := convertCiliumDefaultDeny(defaultDeny)
	require.NotNil(t, result)
	require.NotNil(t, result.Ingress)
	assert.True(t, *result.Ingress)
	require.NotNil(t, result.Egress)
	assert.False(t, *result.Egress)
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

	// ApiVersion
	require.NotNil(t, result.ApiVersion)
	assert.Equal(t, "cilium.io/v2", *result.ApiVersion)

	// EndpointSelector
	require.NotNil(t, result.EndpointSelector)
	assert.Equal(t, "web", result.EndpointSelector.MatchLabels["app"])

	// NodeSelector
	require.NotNil(t, result.NodeSelector)
	assert.Equal(t, "worker", result.NodeSelector.MatchLabels["node-type"])

	// Description
	require.NotNil(t, result.Description)
	assert.Equal(t, "Test policy", *result.Description)

	// Labels
	assert.Equal(t, "security", result.Labels["policy-type"])

	// EnableDefaultDeny
	require.NotNil(t, result.EnableDefaultDeny)
	require.NotNil(t, result.EnableDefaultDeny.Ingress)
	assert.True(t, *result.EnableDefaultDeny.Ingress)

	// Ingress rules
	require.Len(t, result.IngressRules, 1)
	assert.Contains(t, result.IngressRules[0].FromEntities, "cluster")

	// Egress rules
	require.Len(t, result.EgressRules, 1)
	assert.Contains(t, result.EgressRules[0].ToEntities, "world")

	// IngressDeny rules
	require.Len(t, result.IngressDenyRules, 1)
	assert.Contains(t, result.IngressDenyRules[0].FromEntities, "world")

	// EgressDeny rules
	require.Len(t, result.EgressDenyRules, 1)
	assert.Contains(t, result.EgressDenyRules[0].ToCidr, "10.0.0.0/8")
}

// Tests for nil/empty handling

func TestConvertCiliumLabelSelector_Nil(t *testing.T) {
	result := convertCiliumLabelSelector(nil)
	assert.Nil(t, result)
}

func TestConvertCiliumLabelSelector_Empty(t *testing.T) {
	result := convertCiliumLabelSelector(map[string]any{})
	require.NotNil(t, result)
	assert.Empty(t, result.MatchLabels)
	assert.Empty(t, result.MatchExpressions)
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
	assert.Equal(t, "frontend", result[0].MatchLabels["app"])
	assert.Equal(t, "backend", result[1].MatchLabels["app"])
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
	assert.Equal(t, "valid", result[0].MatchLabels["app"])
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
	// Should have empty selector (neither oneof set)
	assert.Nil(t, result[0].GetSelector())
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
	require.Len(t, result[0].Ports, 2)
	assert.Equal(t, "80", result[0].Ports[0].Port)
	assert.Equal(t, "443", result[0].Ports[1].Port)
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
	require.Len(t, rule.FromEndpoints, 1)
	assert.Equal(t, "backend", rule.FromEndpoints[0].MatchLabels["app"])

	// FromCIDR
	require.Len(t, rule.FromCidr, 1)
	assert.Equal(t, "10.0.0.0/8", rule.FromCidr[0])

	// FromCIDRSet
	require.Len(t, rule.FromCidrSet, 1)
	assert.Equal(t, "192.168.0.0/16", rule.FromCidrSet[0].Cidr)

	// FromEntities
	require.Len(t, rule.FromEntities, 1)
	assert.Equal(t, "cluster", rule.FromEntities[0])

	// FromGroups
	require.Len(t, rule.FromGroups, 1)
	require.NotNil(t, rule.FromGroups[0].GetAws())

	// FromNodes
	require.Len(t, rule.FromNodes, 1)
	assert.Equal(t, "worker", rule.FromNodes[0].MatchLabels["node-role"])

	// ToPorts
	require.Len(t, rule.ToPorts, 1)

	// ICMPs
	require.Len(t, rule.Icmps, 1)

	// Authentication
	require.NotNil(t, rule.Authentication)
	assert.Equal(t, "required", rule.Authentication.Mode)
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
	assert.Error(t, err)
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
	require.Len(t, rule.ToEndpoints, 1)
	assert.Equal(t, "database", rule.ToEndpoints[0].MatchLabels["app"])

	// ToCIDR
	require.Len(t, rule.ToCidr, 1)
	assert.Equal(t, "0.0.0.0/0", rule.ToCidr[0])

	// ToCIDRSet
	require.Len(t, rule.ToCidrSet, 1)
	assert.Equal(t, "10.0.0.0/8", rule.ToCidrSet[0].Cidr)

	// ToEntities
	require.Len(t, rule.ToEntities, 1)
	assert.Equal(t, "world", rule.ToEntities[0])

	// ToFQDNs
	require.Len(t, rule.ToFqdns, 1)
	assert.Equal(t, "api.example.com", rule.ToFqdns[0].GetMatchName())

	// ToServices
	require.Len(t, rule.ToServices, 1)
	require.NotNil(t, rule.ToServices[0].K8SService)
	assert.Equal(t, "my-svc", *rule.ToServices[0].K8SService)

	// ToGroups
	require.Len(t, rule.ToGroups, 1)
	require.NotNil(t, rule.ToGroups[0].GetAws())

	// ToNodes
	require.Len(t, rule.ToNodes, 1)
	assert.Equal(t, "egress", rule.ToNodes[0].MatchLabels["node-type"])

	// ToPorts
	require.Len(t, rule.ToPorts, 1)

	// ICMPs
	require.Len(t, rule.Icmps, 1)

	// Authentication
	require.NotNil(t, rule.Authentication)
	assert.Equal(t, "disabled", rule.Authentication.Mode)
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
	assert.Error(t, err)
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

	require.Len(t, result.OwnerReferences, 1)
	assert.Equal(t, "apps/v1", result.OwnerReferences[0].ApiVersion)
	assert.Equal(t, "Deployment", result.OwnerReferences[0].Kind)
	assert.Equal(t, "my-deployment", result.OwnerReferences[0].Name)
	assert.Equal(t, "owner-uid-123", result.OwnerReferences[0].Uid)
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

	assert.Equal(t, "myapp", result.Labels["app"])
	assert.Equal(t, "v1", result.Labels["version"])
	assert.Equal(t, "Test policy annotation", result.Annotations["description"])
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
	assert.Equal(t, "valid", result[0].Key)
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

	assert.Equal(t, "CiliumClusterwideNetworkPolicy", result.Kind)
	assert.Equal(t, "deny-api-server-default", result.Name)

	ccnp := result.GetCiliumClusterwideNetworkPolicy()
	require.NotNil(t, ccnp)
	require.Len(t, ccnp.Specs, 1)

	spec := ccnp.Specs[0]

	// Verify description
	require.NotNil(t, spec.Description)
	assert.Contains(t, *spec.Description, "deny all pods")

	// Verify matchExpressions with NotIn operator
	require.NotNil(t, spec.EndpointSelector)
	require.Len(t, spec.EndpointSelector.MatchExpressions, 1)
	expr := spec.EndpointSelector.MatchExpressions[0]
	assert.Equal(t, "api-access", expr.Key)
	assert.Equal(t, "NotIn", expr.Operator)
	assert.Contains(t, expr.Values, "true")

	// Verify egressDeny rules
	require.Len(t, spec.EgressDenyRules, 2)

	// First deny rule: toEntities kube-apiserver
	assert.Contains(t, spec.EgressDenyRules[0].ToEntities, "kube-apiserver")

	// Second deny rule: toServices kubernetes
	require.Len(t, spec.EgressDenyRules[1].ToServices, 1)
	require.NotNil(t, spec.EgressDenyRules[1].ToServices[0].K8SService)
	assert.Equal(t, "kubernetes", *spec.EgressDenyRules[1].ToServices[0].K8SService)
	require.NotNil(t, spec.EgressDenyRules[1].ToServices[0].K8SNamespace)
	assert.Equal(t, "default", *spec.EgressDenyRules[1].ToServices[0].K8SNamespace)
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
	require.Len(t, ccnp.Specs, 1)

	spec := ccnp.Specs[0]

	// Verify empty endpointSelector (selects all pods)
	require.NotNil(t, spec.EndpointSelector)
	assert.Empty(t, spec.EndpointSelector.MatchLabels)
	assert.Empty(t, spec.EndpointSelector.MatchExpressions)

	// Verify egress rules
	require.Len(t, spec.EgressRules, 3)

	// Rule 1: toEndpoints with empty selector
	require.Len(t, spec.EgressRules[0].ToEndpoints, 1)
	assert.Empty(t, spec.EgressRules[0].ToEndpoints[0].MatchLabels)

	// Rule 2: toEntities host
	assert.Contains(t, spec.EgressRules[1].ToEntities, "host")
	require.Len(t, spec.EgressRules[1].ToPorts, 1)

	// Rule 3: toEntities world
	assert.Contains(t, spec.EgressRules[2].ToEntities, "world")
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
	require.Len(t, ccnp.Specs, 1)

	spec := ccnp.Specs[0]

	// Verify egress rule with toCIDRSet
	require.Len(t, spec.EgressRules, 1)
	rule := spec.EgressRules[0]

	// Verify toCIDRSet with multiple CIDRs and exclusions
	require.Len(t, rule.ToCidrSet, 2)

	// First CIDR with exclusions
	assert.Equal(t, "52.0.0.0/8", rule.ToCidrSet[0].Cidr)
	require.Len(t, rule.ToCidrSet[0].Except, 3)
	assert.Contains(t, rule.ToCidrSet[0].Except, "52.94.0.0/22")
	assert.Contains(t, rule.ToCidrSet[0].Except, "52.119.224.0/20")
	assert.Contains(t, rule.ToCidrSet[0].Except, "52.95.0.0/20")

	// Second CIDR without exclusions
	assert.Equal(t, "54.231.0.0/16", rule.ToCidrSet[1].Cidr)
	assert.Empty(t, rule.ToCidrSet[1].Except)

	// Verify toPorts
	require.Len(t, rule.ToPorts, 1)
	require.Len(t, rule.ToPorts[0].Ports, 1)
	assert.Equal(t, "443", rule.ToPorts[0].Ports[0].Port)
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
	require.Len(t, ccnp.Specs, 1)

	spec := ccnp.Specs[0]

	// Verify egress rules with toFQDNs
	require.Len(t, spec.EgressRules, 2)

	// First rule: S3 with multiple wildcard patterns
	s3Rule := spec.EgressRules[0]
	require.Len(t, s3Rule.ToFqdns, 4)
	assert.Equal(t, "*.s3.amazonaws.com", s3Rule.ToFqdns[0].GetMatchPattern())
	assert.Equal(t, "s3.amazonaws.com", s3Rule.ToFqdns[1].GetMatchPattern())
	assert.Equal(t, "*.s3.*.amazonaws.com", s3Rule.ToFqdns[2].GetMatchPattern())
	assert.Equal(t, "s3.*.amazonaws.com", s3Rule.ToFqdns[3].GetMatchPattern())

	// Second rule: DynamoDB
	dynamoRule := spec.EgressRules[1]
	require.Len(t, dynamoRule.ToFqdns, 1)
	assert.Equal(t, "dynamodb.*.amazonaws.com", dynamoRule.ToFqdns[0].GetMatchPattern())
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
	require.Len(t, ccnp.Specs, 1)

	spec := ccnp.Specs[0]

	// Verify endpoint selector targets data-cache
	require.NotNil(t, spec.EndpointSelector)
	assert.Equal(t, "data-cache", spec.EndpointSelector.MatchLabels["k8s:app"])

	// Verify ingress rules
	require.Len(t, spec.IngressRules, 2)

	// First rule: from data-processor
	require.Len(t, spec.IngressRules[0].FromEndpoints, 1)
	assert.Equal(t, "data-processor", spec.IngressRules[0].FromEndpoints[0].MatchLabels["k8s:role"])

	// Second rule: from report-gen
	require.Len(t, spec.IngressRules[1].FromEndpoints, 1)
	assert.Equal(t, "report-gen", spec.IngressRules[1].FromEndpoints[0].MatchLabels["k8s:role"])
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
