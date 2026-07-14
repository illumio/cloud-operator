// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
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
	result, err := ConvertUnstructuredToAWSResource(zap.NewNop(), nil)
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

	result, err := ConvertUnstructuredToAWSResource(zap.NewNop(), obj)
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

	result, err := ConvertUnstructuredToAWSResource(zap.NewNop(), obj)
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

	result, err := ConvertUnstructuredToAWSResource(zap.NewNop(), obj)
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

	result, err := ConvertUnstructuredToAWSResource(zap.NewNop(), obj)
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

	result, err := ConvertUnstructuredToAWSResource(zap.NewNop(), obj)
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

	result, err := ConvertUnstructuredToAWSResource(zap.NewNop(), obj)
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

	result, err := ConvertUnstructuredToAWSResource(zap.NewNop(), obj)
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

	result, err := ConvertUnstructuredToAWSResource(zap.NewNop(), obj)
	require.NoError(t, err)

	sel := result.GetAwsClusterNetworkPolicy().GetSubject().GetNamespaces()
	require.NotNil(t, sel)
	require.Len(t, sel.GetMatchExpressions(), 1)
	expr := sel.GetMatchExpressions()[0]
	assert.Equal(t, "tier", expr.GetKey())
	assert.Equal(t, "In", expr.GetOperator())
	assert.Equal(t, []string{"frontend", "backend"}, expr.GetValues())
}

func TestIsAWSResource_ApplicationNetworkPolicy(t *testing.T) {
	assert.True(t, IsAWSResource("ApplicationNetworkPolicy"))
	assert.True(t, IsAWSResource("applicationnetworkpolicies"))
}

// newANP builds an unstructured ApplicationNetworkPolicy (namespaced) with the given spec.
func newANP(name, namespace string, spec map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "networking.k8s.aws/v1alpha1",
			"kind":       "ApplicationNetworkPolicy",
			"metadata": map[string]any{
				"name":            name,
				"namespace":       namespace,
				"uid":             "anp-uid",
				"resourceVersion": "67890",
			},
			"spec": spec,
		},
	}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "networking.k8s.aws",
		Version: "v1alpha1",
		Kind:    "ApplicationNetworkPolicy",
	})

	return obj
}

func TestConvertUnstructuredToAWSResource_ApplicationNetworkPolicy_Metadata(t *testing.T) {
	obj := newANP("test-anp", "team-a", map[string]any{
		"podSelector": map[string]any{"matchLabels": map[string]any{"app": "web"}},
		"policyTypes": []any{"Ingress", "Egress"},
	})

	result, err := ConvertUnstructuredToAWSResource(zap.NewNop(), obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "test-anp", result.GetName())
	assert.Equal(t, "team-a", result.GetNamespace()) // namespaced
	assert.Equal(t, "ApplicationNetworkPolicy", result.GetKind())
	assert.Equal(t, "networking.k8s.aws", result.GetApiGroup())

	anp := result.GetAwsApplicationNetworkPolicy()
	require.NotNil(t, anp)
	assert.ElementsMatch(t, []string{"Ingress", "Egress"}, anp.GetPolicyTypes())
	require.NotNil(t, anp.GetPodSelector())
	assert.Equal(t, "web", anp.GetPodSelector().GetMatchLabels()["app"])
}

func TestConvertUnstructuredToAWSResource_ApplicationNetworkPolicy_UnknownPolicyType(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	obj := newANP("weird-anp", "team-a", map[string]any{
		"podSelector": map[string]any{},
		"policyTypes": []any{"Ingress", "Bogus"},
	})

	result, err := ConvertUnstructuredToAWSResource(logger, obj)
	require.NoError(t, err)

	// Unknown types are passed through unchanged, not dropped.
	anp := result.GetAwsApplicationNetworkPolicy()
	assert.Equal(t, []string{"Ingress", "Bogus"}, anp.GetPolicyTypes())

	// The unexpected value is logged as a warning.
	warnings := logs.FilterMessage("Unknown ApplicationNetworkPolicy policy type").All()
	require.Len(t, warnings, 1)
	assert.Equal(t, "Bogus", warnings[0].ContextMap()["policy_type"])
}

func TestConvertUnstructuredToAWSResource_ApplicationNetworkPolicy_IngressRules(t *testing.T) {
	obj := newANP("ingress-anp", "default", map[string]any{
		"podSelector": map[string]any{"matchLabels": map[string]any{"app": "db"}},
		"policyTypes": []any{"Ingress"},
		"ingress": []any{
			map[string]any{
				"ports": []any{
					map[string]any{"port": int64(5432), "protocol": "TCP"},
				},
				"from": []any{
					map[string]any{
						"podSelector": map[string]any{"matchLabels": map[string]any{"app": "api"}},
					},
					map[string]any{
						"ipBlock": map[string]any{"cidr": "10.0.0.0/8", "except": []any{"10.1.0.0/16"}},
					},
				},
			},
		},
	})

	result, err := ConvertUnstructuredToAWSResource(zap.NewNop(), obj)
	require.NoError(t, err)

	anp := result.GetAwsApplicationNetworkPolicy()
	require.Len(t, anp.GetIngress(), 1)
	rule := anp.GetIngress()[0]

	require.Len(t, rule.GetPorts(), 1)
	assert.InDelta(t, 5432, rule.GetPorts()[0].GetPort().GetNumberValue(), 0)
	assert.Equal(t, "TCP", rule.GetPorts()[0].GetProtocol())

	require.Len(t, rule.GetFrom(), 2)
	assert.Equal(t, "api", rule.GetFrom()[0].GetPodSelector().GetMatchLabels()["app"])
	require.NotNil(t, rule.GetFrom()[1].GetIpBlock())
	assert.Equal(t, "10.0.0.0/8", rule.GetFrom()[1].GetIpBlock().GetCidr())
	assert.Equal(t, []string{"10.1.0.0/16"}, rule.GetFrom()[1].GetIpBlock().GetExcept())
}

func TestConvertUnstructuredToAWSResource_ApplicationNetworkPolicy_EgressDomainNames(t *testing.T) {
	obj := newANP("egress-anp", "default", map[string]any{
		"podSelector": map[string]any{"matchLabels": map[string]any{"security-tier": "low"}},
		"policyTypes": []any{"Egress"},
		"egress": []any{
			map[string]any{
				"to": []any{
					map[string]any{
						"domainNames": []any{"*.s3.us-east-1.amazonaws.com", "api.example.com"},
					},
					map[string]any{
						"namespaceSelector": map[string]any{"matchLabels": map[string]any{"env": "prod"}},
					},
				},
			},
		},
	})

	result, err := ConvertUnstructuredToAWSResource(zap.NewNop(), obj)
	require.NoError(t, err)

	anp := result.GetAwsApplicationNetworkPolicy()
	require.Len(t, anp.GetEgress(), 1)
	peers := anp.GetEgress()[0].GetTo()
	require.Len(t, peers, 2)

	assert.Equal(t, []string{"*.s3.us-east-1.amazonaws.com", "api.example.com"}, peers[0].GetDomainNames())
	assert.Equal(t, "prod", peers[1].GetNamespaceSelector().GetMatchLabels()["env"])
}

func TestConvertUnstructuredToAWSResource_ApplicationNetworkPolicy_RoundTrip(t *testing.T) {
	inputSpec := map[string]any{
		"podSelector": map[string]any{}, // empty {} = all pods in namespace
		"policyTypes": []any{"Ingress", "Egress"},
		"ingress": []any{
			map[string]any{
				"ports": []any{
					map[string]any{"port": int64(5432), "protocol": "TCP"}, // numeric port
					map[string]any{"port": "https"},                        // named port
				},
				"from": []any{
					map[string]any{"podSelector": map[string]any{}}, // empty {} = all pods
					map[string]any{"namespaceSelector": map[string]any{"matchLabels": map[string]any{"env": "prod"}}},
					map[string]any{"ipBlock": map[string]any{"cidr": "10.0.0.0/8", "except": []any{"10.1.0.0/16"}}},
				},
			},
		},
		"egress": []any{
			map[string]any{
				"to": []any{
					map[string]any{"domainNames": []any{"*.amazonaws.com"}},
				},
			},
		},
	}

	obj := newANP("rt-anp", "team-a", inputSpec)

	// CRD → proto.
	meta, err := ConvertUnstructuredToAWSResource(zap.NewNop(), obj)
	require.NoError(t, err)

	// proto (metadata) → proto (configured), as done before reconcile apply.
	configured, err := BuildConfiguredFromMetadata("rt-id", meta)
	require.NoError(t, err)

	// proto → applied CRD.
	apply, resourceName, err := ConvertToApplyObject(configured, "networking.k8s.aws", "v1alpha1")
	require.NoError(t, err)

	assert.Equal(t, "applicationnetworkpolicies", resourceName)
	assert.Equal(t, "ApplicationNetworkPolicy", apply.GetKind())

	appliedSpec, ok := apply.Object["spec"].(map[string]any)
	require.True(t, ok, "expected spec to be a map")

	// The whole spec must survive the round-trip byte-for-byte (after JSON
	// normalization of numeric types), catching any dropped or mutated field.
	assert.Equal(t, normalizeJSON(t, inputSpec), normalizeJSON(t, appliedSpec))

	// Explicit checks on the fix-critical invariants:
	// 1. Empty podSelector is preserved as {} (top-level and peer), not dropped.
	assert.Equal(t, map[string]any{}, appliedSpec["podSelector"], "empty podSelector must stay {}")

	ingress, ok := appliedSpec["ingress"].([]any)
	require.True(t, ok)
	rule, ok := ingress[0].(map[string]any)
	require.True(t, ok)

	from, ok := rule["from"].([]any)
	require.True(t, ok)
	peer0, ok := from[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{}, peer0["podSelector"], "empty peer podSelector must stay {}")

	// 2. Ports serialize as intstr: bare number for numeric, bare string for named.
	ports, ok := rule["ports"].([]any)
	require.True(t, ok)
	port0, ok := ports[0].(map[string]any)
	require.True(t, ok)
	port1, ok := ports[1].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 5432, port0["port"], 0)
	assert.Equal(t, "https", port1["port"])
}

func TestConvertUnstructuredToAWSResource_ClusterNetworkPolicy_RoundTrip(t *testing.T) {
	inputSpec := map[string]any{
		"priority": int64(100),
		"tier":     "Admin",
		"subject": map[string]any{
			"namespaces": map[string]any{"matchLabels": map[string]any{"env": "prod"}},
		},
		"ingress": []any{
			map[string]any{
				"name":   "allow-frontend",
				"action": "Accept",
				"ports": []any{
					map[string]any{"portNumber": map[string]any{"protocol": "TCP", "port": int64(443)}},
					map[string]any{"portRange": map[string]any{"protocol": "TCP", "start": int64(8000), "end": int64(9000)}},
					map[string]any{"namedPort": "http"},
				},
				"from": []any{
					map[string]any{"namespaces": map[string]any{"matchLabels": map[string]any{"team": "frontend"}}},
					map[string]any{"pods": map[string]any{
						"namespaceSelector": map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": "ns"}},
						"podSelector":       map[string]any{"matchLabels": map[string]any{"app": "web"}},
					}},
				},
			},
		},
		"egress": []any{
			map[string]any{
				"action": "Pass",
				"to": []any{
					map[string]any{
						"networks":    []any{"10.0.0.0/8"},
						"domainNames": []any{"*.amazonaws.com"},
					},
				},
			},
		},
	}

	obj := newCNP("rt-cnp", inputSpec)

	// CRD → proto.
	meta, err := ConvertUnstructuredToAWSResource(zap.NewNop(), obj)
	require.NoError(t, err)

	// proto (metadata) → proto (configured), as done before reconcile apply.
	configured, err := BuildConfiguredFromMetadata("rt-cnp-id", meta)
	require.NoError(t, err)

	// proto → applied CRD.
	apply, resourceName, err := ConvertToApplyObject(configured, "networking.k8s.aws", "v1alpha1")
	require.NoError(t, err)

	assert.Equal(t, "clusternetworkpolicies", resourceName)
	assert.Equal(t, "ClusterNetworkPolicy", apply.GetKind())
	assert.Empty(t, apply.GetNamespace()) // cluster-scoped

	appliedSpec, ok := apply.Object["spec"].(map[string]any)
	require.True(t, ok, "expected spec to be a map")

	// The whole spec must survive the round-trip byte-for-byte (after JSON
	// normalization of numeric types), catching any dropped or mutated field.
	assert.Equal(t, normalizeJSON(t, inputSpec), normalizeJSON(t, appliedSpec))
}

func TestConvertUnstructuredToAWSResource_ClusterNetworkPolicy_PriorityUnset(t *testing.T) {
	// No "priority" key in the spec.
	obj := newCNP("no-priority", map[string]any{
		"tier":    "Admin",
		"subject": map[string]any{"namespaces": map[string]any{"matchLabels": map[string]any{}}},
	})

	meta, err := ConvertUnstructuredToAWSResource(zap.NewNop(), obj)
	require.NoError(t, err)

	configured, err := BuildConfiguredFromMetadata("no-priority-id", meta)
	require.NoError(t, err)

	apply, _, err := ConvertToApplyObject(configured, "networking.k8s.aws", "v1alpha1")
	require.NoError(t, err)

	appliedSpec, ok := apply.Object["spec"].(map[string]any)
	require.True(t, ok)

	priority, present := appliedSpec["priority"]
	require.True(t, present, "unset priority must still be enforced as 0, not dropped")
	assert.EqualValues(t, 0, priority)
}

func normalizeJSON(t *testing.T, v any) map[string]any {
	t.Helper()

	b, err := json.Marshal(v)
	require.NoError(t, err)

	var out map[string]any

	require.NoError(t, json.Unmarshal(b, &out))

	return out
}
