// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

func TestIsAWSNetworkPolicy(t *testing.T) {
	tests := map[string]struct {
		kind     string
		expected bool
	}{
		"ClusterNetworkPolicy": {
			kind:     "ClusterNetworkPolicy",
			expected: true,
		},
		"SecurityGroupPolicy": {
			kind:     "SecurityGroupPolicy",
			expected: true,
		},
		"NetworkPolicy": {
			kind:     "NetworkPolicy",
			expected: false,
		},
		"CiliumNetworkPolicy": {
			kind:     "CiliumNetworkPolicy",
			expected: false,
		},
		"Pod": {
			kind:     "Pod",
			expected: false,
		},
		"empty string": {
			kind:     "",
			expected: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := IsAWSNetworkPolicy(tt.kind)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertUnstructuredToAWSNetworkPolicy_ClusterNetworkPolicy(t *testing.T) {
	creationTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "networking.k8s.aws/v1alpha1",
			"kind":       "ClusterNetworkPolicy",
			"metadata": map[string]any{
				"name":              "test-cnp",
				"uid":               "test-uid-123",
				"resourceVersion":   "12345",
				"creationTimestamp": creationTime.Format(time.RFC3339),
				"labels": map[string]any{
					"app": "test",
				},
				"annotations": map[string]any{
					"description": "test policy",
				},
			},
			"spec": map[string]any{
				"priority": int64(20),
				"tier":     "Admin",
				"subject": map[string]any{
					"pods": map[string]any{
						"namespaceSelector": map[string]any{
							"matchLabels": map[string]any{
								"kubernetes.io/metadata.name": "france",
							},
						},
						"podSelector": map[string]any{
							"matchLabels": map[string]any{
								"city": "paris",
							},
						},
					},
				},
				"egress": []any{
					map[string]any{
						"action": "Accept",
						"ports": []any{
							map[string]any{
								"portNumber": map[string]any{
									"port":     int64(5432),
									"protocol": "TCP",
								},
							},
						},
						"to": []any{
							map[string]any{
								"networks": []any{"10.0.111.111/32"},
							},
						},
					},
				},
			},
		},
	}

	result, err := ConvertUnstructuredToAWSNetworkPolicy(obj)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "ClusterNetworkPolicy", result.GetKind())
	assert.Equal(t, "test-cnp", result.GetName())
	assert.Equal(t, "test-uid-123", result.GetUid())
	assert.Equal(t, "12345", result.GetResourceVersion())
	assert.Equal(t, "test", result.GetLabels()["app"])
	assert.Equal(t, "test policy", result.GetAnnotations()["description"])

	cnp := result.GetAwsClusterNetworkPolicy()
	require.NotNil(t, cnp)
	assert.Equal(t, int32(20), cnp.GetPriority())
	assert.Equal(t, "Admin", cnp.GetTier())

	// Check subject
	require.NotNil(t, cnp.GetSubject())
	require.NotNil(t, cnp.GetSubject().GetPods())
	assert.Equal(t, "france", cnp.GetSubject().GetPods().GetNamespaceSelector().GetMatchLabels()["kubernetes.io/metadata.name"])
	assert.Equal(t, "paris", cnp.GetSubject().GetPods().GetPodSelector().GetMatchLabels()["city"])

	// Check egress rules
	require.Len(t, cnp.GetEgressRules(), 1)
	assert.Equal(t, "Accept", cnp.GetEgressRules()[0].GetAction())
	require.Len(t, cnp.GetEgressRules()[0].GetPorts(), 1)
	assert.Equal(t, int32(5432), cnp.GetEgressRules()[0].GetPorts()[0].GetPortNumber().GetPort())
	assert.Equal(t, "TCP", cnp.GetEgressRules()[0].GetPorts()[0].GetPortNumber().GetProtocol())
	require.Len(t, cnp.GetEgressRules()[0].GetTo(), 1)
	assert.Equal(t, []string{"10.0.111.111/32"}, cnp.GetEgressRules()[0].GetTo()[0].GetNetworks())
}

func TestConvertUnstructuredToAWSNetworkPolicy_SecurityGroupPolicy(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "vpcresources.k8s.aws/v1beta1",
			"kind":       "SecurityGroupPolicy",
			"metadata": map[string]any{
				"name":            "sgp-test",
				"namespace":       "france",
				"uid":             "sgp-uid-456",
				"resourceVersion": "67890",
			},
			"spec": map[string]any{
				"podSelector": map[string]any{
					"matchLabels": map[string]any{
						"city": "paris",
					},
				},
				"securityGroups": map[string]any{
					"groupIds": []any{
						"sg-0b49113ccd79c7676",
						"sg-0d036e547ceaa8ee9",
					},
				},
			},
		},
	}

	result, err := ConvertUnstructuredToAWSNetworkPolicy(obj)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "SecurityGroupPolicy", result.GetKind())
	assert.Equal(t, "sgp-test", result.GetName())
	assert.Equal(t, "france", result.GetNamespace())

	sgp := result.GetAwsSecurityGroupPolicy()
	require.NotNil(t, sgp)
	assert.Equal(t, "paris", sgp.GetPodSelector().GetMatchLabels()["city"])
	assert.Equal(t, []string{"sg-0b49113ccd79c7676", "sg-0d036e547ceaa8ee9"}, sgp.GetSecurityGroupIds())
}

func TestConvertUnstructuredToAWSNetworkPolicy_NilInput(t *testing.T) {
	result, err := ConvertUnstructuredToAWSNetworkPolicy(nil)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestConvertUnstructuredToAWSNetworkPolicy_NoSpec(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "networking.k8s.aws/v1alpha1",
			"kind":       "ClusterNetworkPolicy",
			"metadata": map[string]any{
				"name": "test-cnp-no-spec",
			},
		},
	}

	result, err := ConvertUnstructuredToAWSNetworkPolicy(obj)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "ClusterNetworkPolicy", result.GetKind())
	assert.Equal(t, "test-cnp-no-spec", result.GetName())
	// No kind_specific should be set since there's no spec
	assert.Nil(t, result.GetAwsClusterNetworkPolicy())
}

func TestConvertAWSClusterNetworkPolicySpec_WithNamespaceSubject(t *testing.T) {
	// Test Scenario 7/8 from POC - namespace-based subject
	spec := map[string]any{
		"priority": int64(20),
		"tier":     "Admin",
		"subject": map[string]any{
			"namespaces": map[string]any{
				"matchLabels": map[string]any{
					"kubernetes.io/metadata.name": "france",
				},
			},
		},
		"egress": []any{
			map[string]any{
				"action": "Accept",
				"to": []any{
					map[string]any{
						"pods": map[string]any{
							"namespaceSelector": map[string]any{
								"matchLabels": map[string]any{
									"kubernetes.io/metadata.name": "japan",
								},
							},
							"podSelector": map[string]any{},
						},
					},
				},
			},
		},
	}

	result, err := convertAWSClusterNetworkPolicySpec(spec)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(20), result.GetPriority())
	assert.Equal(t, "Admin", result.GetTier())

	// Check namespace-based subject
	require.NotNil(t, result.GetSubject())
	require.NotNil(t, result.GetSubject().GetNamespaces())
	assert.Equal(t, "france", result.GetSubject().GetNamespaces().GetMatchLabels()["kubernetes.io/metadata.name"])

	// Check egress rules
	require.Len(t, result.GetEgressRules(), 1)
	require.Len(t, result.GetEgressRules()[0].GetTo(), 1)
	assert.Equal(t, "japan", result.GetEgressRules()[0].GetTo()[0].GetPods().GetNamespaceSelector().GetMatchLabels()["kubernetes.io/metadata.name"])
}

func TestConvertAWSClusterNetworkPolicySpec_WithDomainNames(t *testing.T) {
	// Test Scenario 9 from POC - FQDN support
	spec := map[string]any{
		"priority": int64(20),
		"tier":     "Admin",
		"subject": map[string]any{
			"pods": map[string]any{
				"namespaceSelector": map[string]any{
					"matchLabels": map[string]any{
						"kubernetes.io/metadata.name": "france",
					},
				},
				"podSelector": map[string]any{
					"matchLabels": map[string]any{
						"city": "lyon",
					},
				},
			},
		},
		"egress": []any{
			map[string]any{
				"action": "Accept",
				"ports": []any{
					map[string]any{
						"portNumber": map[string]any{
							"port":     int64(443),
							"protocol": "TCP",
						},
					},
				},
				"to": []any{
					map[string]any{
						"domainNames": []any{
							"google.com",
							"*.google.com",
						},
					},
				},
			},
		},
	}

	result, err := convertAWSClusterNetworkPolicySpec(spec)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.GetEgressRules(), 1)
	require.Len(t, result.GetEgressRules()[0].GetTo(), 1)
	assert.Equal(t, []string{"google.com", "*.google.com"}, result.GetEgressRules()[0].GetTo()[0].GetDomainNames())
}

func TestConvertAWSClusterNetworkPolicySpec_WithIngressRules(t *testing.T) {
	// Test Scenario 2 from POC - ingress rules
	spec := map[string]any{
		"priority": int64(20),
		"tier":     "Admin",
		"subject": map[string]any{
			"pods": map[string]any{
				"namespaceSelector": map[string]any{
					"matchLabels": map[string]any{
						"kubernetes.io/metadata.name": "france",
					},
				},
				"podSelector": map[string]any{
					"matchLabels": map[string]any{
						"city": "nice",
					},
				},
			},
		},
		"ingress": []any{
			map[string]any{
				"action": "Accept",
				"from": []any{
					map[string]any{
						"pods": map[string]any{
							"namespaceSelector": map[string]any{
								"matchLabels": map[string]any{
									"kubernetes.io/metadata.name": "france",
								},
							},
							"podSelector": map[string]any{
								"matchLabels": map[string]any{
									"city": "paris",
								},
							},
						},
					},
				},
			},
		},
	}

	result, err := convertAWSClusterNetworkPolicySpec(spec)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.GetIngressRules(), 1)
	assert.Equal(t, "Accept", result.GetIngressRules()[0].GetAction())
	require.Len(t, result.GetIngressRules()[0].GetFrom(), 1)
	assert.Equal(t, "paris", result.GetIngressRules()[0].GetFrom()[0].GetPods().GetPodSelector().GetMatchLabels()["city"])
}

func TestConvertAWSClusterNetworkPolicySpec_DenyAllEgress(t *testing.T) {
	// Test deny-all pattern from POC
	spec := map[string]any{
		"priority": int64(100),
		"tier":     "Admin",
		"subject": map[string]any{
			"pods": map[string]any{
				"namespaceSelector": map[string]any{
					"matchLabels": map[string]any{
						"kubernetes.io/metadata.name": "france",
					},
				},
				"podSelector": map[string]any{
					"matchLabels": map[string]any{
						"city": "paris",
					},
				},
			},
		},
		"egress": []any{
			map[string]any{
				"action": "Deny",
				"to": []any{
					map[string]any{
						"networks": []any{"0.0.0.0/0"},
					},
				},
			},
		},
	}

	result, err := convertAWSClusterNetworkPolicySpec(spec)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(100), result.GetPriority())
	require.Len(t, result.GetEgressRules(), 1)
	assert.Equal(t, "Deny", result.GetEgressRules()[0].GetAction())
	assert.Equal(t, []string{"0.0.0.0/0"}, result.GetEgressRules()[0].GetTo()[0].GetNetworks())
}

func TestConvertAWSSecurityGroupPolicySpec(t *testing.T) {
	tests := map[string]struct {
		spec           map[string]any
		expectedPolicy *pb.KubernetesAWSSecurityGroupPolicyData
	}{
		"basic SGP with podSelector": {
			spec: map[string]any{
				"podSelector": map[string]any{
					"matchLabels": map[string]any{
						"city": "paris",
					},
				},
				"securityGroups": map[string]any{
					"groupIds": []any{
						"sg-0b49113ccd79c7676",
						"sg-0d036e547ceaa8ee9",
					},
				},
			},
			expectedPolicy: &pb.KubernetesAWSSecurityGroupPolicyData{
				PodSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{"city": "paris"},
				},
				SecurityGroupIds: []string{"sg-0b49113ccd79c7676", "sg-0d036e547ceaa8ee9"},
			},
		},
		"SGP with serviceAccountSelector": {
			spec: map[string]any{
				"serviceAccountSelector": map[string]any{
					"matchLabels": map[string]any{
						"app": "backend",
					},
				},
				"securityGroups": map[string]any{
					"groupIds": []any{
						"sg-abc123",
					},
				},
			},
			expectedPolicy: &pb.KubernetesAWSSecurityGroupPolicyData{
				ServiceAccountSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{"app": "backend"},
				},
				SecurityGroupIds: []string{"sg-abc123"},
			},
		},
		"empty spec": {
			spec:           map[string]any{},
			expectedPolicy: &pb.KubernetesAWSSecurityGroupPolicyData{},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := convertAWSSecurityGroupPolicySpec(tt.spec)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedPolicy.GetPodSelector(), result.GetPodSelector())
			assert.Equal(t, tt.expectedPolicy.GetServiceAccountSelector(), result.GetServiceAccountSelector())
			assert.Equal(t, tt.expectedPolicy.GetSecurityGroupIds(), result.GetSecurityGroupIds())
		})
	}
}

func TestConvertAWSNetworkPolicySubject(t *testing.T) {
	tests := map[string]struct {
		subject  map[string]any
		checkFn  func(t *testing.T, result *pb.AWSNetworkPolicySubject)
	}{
		"nil subject": {
			subject: nil,
			checkFn: func(t *testing.T, result *pb.AWSNetworkPolicySubject) {
				assert.Nil(t, result)
			},
		},
		"pods subject": {
			subject: map[string]any{
				"pods": map[string]any{
					"namespaceSelector": map[string]any{
						"matchLabels": map[string]any{
							"kubernetes.io/metadata.name": "france",
						},
					},
					"podSelector": map[string]any{
						"matchLabels": map[string]any{
							"city": "paris",
						},
					},
				},
			},
			checkFn: func(t *testing.T, result *pb.AWSNetworkPolicySubject) {
				require.NotNil(t, result)
				require.NotNil(t, result.GetPods())
				assert.Equal(t, "france", result.GetPods().GetNamespaceSelector().GetMatchLabels()["kubernetes.io/metadata.name"])
				assert.Equal(t, "paris", result.GetPods().GetPodSelector().GetMatchLabels()["city"])
			},
		},
		"namespaces subject": {
			subject: map[string]any{
				"namespaces": map[string]any{
					"matchLabels": map[string]any{
						"kubernetes.io/metadata.name": "japan",
					},
				},
			},
			checkFn: func(t *testing.T, result *pb.AWSNetworkPolicySubject) {
				require.NotNil(t, result)
				require.NotNil(t, result.GetNamespaces())
				assert.Equal(t, "japan", result.GetNamespaces().GetMatchLabels()["kubernetes.io/metadata.name"])
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := convertAWSNetworkPolicySubject(tt.subject)
			require.NoError(t, err)
			tt.checkFn(t, result)
		})
	}
}

func TestConvertAWSLabelSelector(t *testing.T) {
	tests := map[string]struct {
		selector map[string]any
		expected *pb.LabelSelector
	}{
		"nil selector": {
			selector: nil,
			expected: nil,
		},
		"matchLabels only": {
			selector: map[string]any{
				"matchLabels": map[string]any{
					"app":  "frontend",
					"tier": "web",
				},
			},
			expected: &pb.LabelSelector{
				MatchLabels: map[string]string{"app": "frontend", "tier": "web"},
			},
		},
		"matchExpressions only": {
			selector: map[string]any{
				"matchExpressions": []any{
					map[string]any{
						"key":      "environment",
						"operator": "In",
						"values":   []any{"production", "staging"},
					},
				},
			},
			expected: &pb.LabelSelector{
				MatchExpressions: []*pb.LabelSelectorRequirement{
					{
						Key:      "environment",
						Operator: "In",
						Values:   []string{"production", "staging"},
					},
				},
			},
		},
		"both matchLabels and matchExpressions": {
			selector: map[string]any{
				"matchLabels": map[string]any{
					"app": "backend",
				},
				"matchExpressions": []any{
					map[string]any{
						"key":      "version",
						"operator": "NotIn",
						"values":   []any{"v1", "v2"},
					},
				},
			},
			expected: &pb.LabelSelector{
				MatchLabels: map[string]string{"app": "backend"},
				MatchExpressions: []*pb.LabelSelectorRequirement{
					{
						Key:      "version",
						Operator: "NotIn",
						Values:   []string{"v1", "v2"},
					},
				},
			},
		},
		"empty selector": {
			selector: map[string]any{},
			expected: &pb.LabelSelector{},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := convertAWSLabelSelector(tt.selector)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertAWSMatchExpressions(t *testing.T) {
	tests := map[string]struct {
		expressions []any
		expected    []*pb.LabelSelectorRequirement
	}{
		"nil expressions": {
			expressions: nil,
			expected:    nil,
		},
		"empty expressions": {
			expressions: []any{},
			expected:    nil,
		},
		"single expression": {
			expressions: []any{
				map[string]any{
					"key":      "app",
					"operator": "Exists",
				},
			},
			expected: []*pb.LabelSelectorRequirement{
				{
					Key:      "app",
					Operator: "Exists",
				},
			},
		},
		"multiple expressions": {
			expressions: []any{
				map[string]any{
					"key":      "env",
					"operator": "In",
					"values":   []any{"prod", "staging"},
				},
				map[string]any{
					"key":      "team",
					"operator": "NotIn",
					"values":   []any{"legacy"},
				},
			},
			expected: []*pb.LabelSelectorRequirement{
				{
					Key:      "env",
					Operator: "In",
					Values:   []string{"prod", "staging"},
				},
				{
					Key:      "team",
					Operator: "NotIn",
					Values:   []string{"legacy"},
				},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := convertAWSMatchExpressions(tt.expressions)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertAWSMatchExpressions_InvalidType(t *testing.T) {
	expressions := []any{
		"invalid",
		map[string]any{
			"key":      "valid",
			"operator": "Exists",
		},
	}

	_, err := convertAWSMatchExpressions(expressions)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matchExpression[0] is not a map")
}

func TestConvertAWSNetworkPolicyRules(t *testing.T) {
	tests := map[string]struct {
		rules    []any
		checkFn  func(t *testing.T, result []*pb.AWSNetworkPolicyRule)
	}{
		"nil rules": {
			rules: nil,
			checkFn: func(t *testing.T, result []*pb.AWSNetworkPolicyRule) {
				assert.Nil(t, result)
			},
		},
		"empty rules": {
			rules: []any{},
			checkFn: func(t *testing.T, result []*pb.AWSNetworkPolicyRule) {
				assert.Nil(t, result)
			},
		},
		"rule with action and ports": {
			rules: []any{
				map[string]any{
					"action": "Accept",
					"ports": []any{
						map[string]any{
							"portNumber": map[string]any{
								"port":     int64(80),
								"protocol": "TCP",
							},
						},
					},
				},
			},
			checkFn: func(t *testing.T, result []*pb.AWSNetworkPolicyRule) {
				require.Len(t, result, 1)
				assert.Equal(t, "Accept", result[0].GetAction())
				require.Len(t, result[0].GetPorts(), 1)
				assert.Equal(t, int32(80), result[0].GetPorts()[0].GetPortNumber().GetPort())
				assert.Equal(t, "TCP", result[0].GetPorts()[0].GetPortNumber().GetProtocol())
			},
		},
		"rule with from peers": {
			rules: []any{
				map[string]any{
					"action": "Accept",
					"from": []any{
						map[string]any{
							"namespaces": map[string]any{
								"matchLabels": map[string]any{
									"kubernetes.io/metadata.name": "france",
								},
							},
						},
					},
				},
			},
			checkFn: func(t *testing.T, result []*pb.AWSNetworkPolicyRule) {
				require.Len(t, result, 1)
				require.Len(t, result[0].GetFrom(), 1)
				assert.Equal(t, "france", result[0].GetFrom()[0].GetNamespaces().GetMatchLabels()["kubernetes.io/metadata.name"])
			},
		},
		"rule with to peers": {
			rules: []any{
				map[string]any{
					"action": "Deny",
					"to": []any{
						map[string]any{
							"networks": []any{"0.0.0.0/0"},
						},
					},
				},
			},
			checkFn: func(t *testing.T, result []*pb.AWSNetworkPolicyRule) {
				require.Len(t, result, 1)
				assert.Equal(t, "Deny", result[0].GetAction())
				require.Len(t, result[0].GetTo(), 1)
				assert.Equal(t, []string{"0.0.0.0/0"}, result[0].GetTo()[0].GetNetworks())
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := convertAWSNetworkPolicyRules(tt.rules)
			require.NoError(t, err)
			tt.checkFn(t, result)
		})
	}
}

func TestConvertAWSNetworkPolicyPorts(t *testing.T) {
	tests := map[string]struct {
		ports    []any
		expected []*pb.AWSNetworkPolicyPort
	}{
		"nil ports": {
			ports:    nil,
			expected: nil,
		},
		"empty ports": {
			ports:    []any{},
			expected: nil,
		},
		"single port": {
			ports: []any{
				map[string]any{
					"portNumber": map[string]any{
						"port":     int64(443),
						"protocol": "TCP",
					},
				},
			},
			expected: []*pb.AWSNetworkPolicyPort{
				{
					PortNumber: &pb.AWSPortNumber{
						Port:     443,
						Protocol: "TCP",
					},
				},
			},
		},
		"multiple ports": {
			ports: []any{
				map[string]any{
					"portNumber": map[string]any{
						"port":     int64(80),
						"protocol": "TCP",
					},
				},
				map[string]any{
					"portNumber": map[string]any{
						"port":     int64(53),
						"protocol": "UDP",
					},
				},
			},
			expected: []*pb.AWSNetworkPolicyPort{
				{
					PortNumber: &pb.AWSPortNumber{
						Port:     80,
						Protocol: "TCP",
					},
				},
				{
					PortNumber: &pb.AWSPortNumber{
						Port:     53,
						Protocol: "UDP",
					},
				},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := convertAWSNetworkPolicyPorts(tt.ports)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertAWSPortNumber(t *testing.T) {
	tests := map[string]struct {
		portNumber map[string]any
		expected   *pb.AWSPortNumber
	}{
		"nil portNumber": {
			portNumber: nil,
			expected:   nil,
		},
		"full portNumber": {
			portNumber: map[string]any{
				"port":     int64(5432),
				"protocol": "TCP",
			},
			expected: &pb.AWSPortNumber{
				Port:     5432,
				Protocol: "TCP",
			},
		},
		"port only": {
			portNumber: map[string]any{
				"port": int64(8080),
			},
			expected: &pb.AWSPortNumber{
				Port: 8080,
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := convertAWSPortNumber(tt.portNumber)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertAWSNetworkPolicyPeers(t *testing.T) {
	tests := map[string]struct {
		peers   []any
		checkFn func(t *testing.T, result []*pb.AWSNetworkPolicyPeer)
	}{
		"nil peers": {
			peers: nil,
			checkFn: func(t *testing.T, result []*pb.AWSNetworkPolicyPeer) {
				assert.Nil(t, result)
			},
		},
		"empty peers": {
			peers: []any{},
			checkFn: func(t *testing.T, result []*pb.AWSNetworkPolicyPeer) {
				assert.Nil(t, result)
			},
		},
		"peer with networks": {
			peers: []any{
				map[string]any{
					"networks": []any{"10.0.0.0/16", "192.168.0.0/24"},
				},
			},
			checkFn: func(t *testing.T, result []*pb.AWSNetworkPolicyPeer) {
				require.Len(t, result, 1)
				assert.Equal(t, []string{"10.0.0.0/16", "192.168.0.0/24"}, result[0].GetNetworks())
			},
		},
		"peer with pods selector": {
			peers: []any{
				map[string]any{
					"pods": map[string]any{
						"namespaceSelector": map[string]any{
							"matchLabels": map[string]any{
								"kubernetes.io/metadata.name": "japan",
							},
						},
						"podSelector": map[string]any{
							"matchLabels": map[string]any{
								"city": "kyoto",
							},
						},
					},
				},
			},
			checkFn: func(t *testing.T, result []*pb.AWSNetworkPolicyPeer) {
				require.Len(t, result, 1)
				assert.Equal(t, "japan", result[0].GetPods().GetNamespaceSelector().GetMatchLabels()["kubernetes.io/metadata.name"])
				assert.Equal(t, "kyoto", result[0].GetPods().GetPodSelector().GetMatchLabels()["city"])
			},
		},
		"peer with namespaces selector": {
			peers: []any{
				map[string]any{
					"namespaces": map[string]any{
						"matchLabels": map[string]any{
							"kubernetes.io/metadata.name": "kube-system",
						},
					},
				},
			},
			checkFn: func(t *testing.T, result []*pb.AWSNetworkPolicyPeer) {
				require.Len(t, result, 1)
				assert.Equal(t, "kube-system", result[0].GetNamespaces().GetMatchLabels()["kubernetes.io/metadata.name"])
			},
		},
		"peer with domainNames": {
			peers: []any{
				map[string]any{
					"domainNames": []any{"google.com", "*.google.com"},
				},
			},
			checkFn: func(t *testing.T, result []*pb.AWSNetworkPolicyPeer) {
				require.Len(t, result, 1)
				assert.Equal(t, []string{"google.com", "*.google.com"}, result[0].GetDomainNames())
			},
		},
		"multiple peers with mixed types": {
			peers: []any{
				map[string]any{
					"networks": []any{"10.0.111.111/32"},
				},
				map[string]any{
					"pods": map[string]any{
						"namespaceSelector": map[string]any{},
						"podSelector":       map[string]any{},
					},
				},
			},
			checkFn: func(t *testing.T, result []*pb.AWSNetworkPolicyPeer) {
				require.Len(t, result, 2)
				assert.Equal(t, []string{"10.0.111.111/32"}, result[0].GetNetworks())
				assert.NotNil(t, result[1].GetPods())
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := convertAWSNetworkPolicyPeers(tt.peers)
			require.NoError(t, err)
			tt.checkFn(t, result)
		})
	}
}

func TestConvertAWSNetworkPolicyPodSelector(t *testing.T) {
	tests := map[string]struct {
		pods     map[string]any
		expected *pb.AWSNetworkPolicyPodSelector
	}{
		"nil pods": {
			pods:     nil,
			expected: nil,
		},
		"full pod selector": {
			pods: map[string]any{
				"namespaceSelector": map[string]any{
					"matchLabels": map[string]any{
						"kubernetes.io/metadata.name": "france",
					},
				},
				"podSelector": map[string]any{
					"matchLabels": map[string]any{
						"city": "paris",
					},
				},
			},
			expected: &pb.AWSNetworkPolicyPodSelector{
				NamespaceSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{"kubernetes.io/metadata.name": "france"},
				},
				PodSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{"city": "paris"},
				},
			},
		},
		"namespace selector only": {
			pods: map[string]any{
				"namespaceSelector": map[string]any{
					"matchLabels": map[string]any{
						"team": "backend",
					},
				},
			},
			expected: &pb.AWSNetworkPolicyPodSelector{
				NamespaceSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{"team": "backend"},
				},
			},
		},
		"empty selectors": {
			pods: map[string]any{
				"namespaceSelector": map[string]any{},
				"podSelector":       map[string]any{},
			},
			expected: &pb.AWSNetworkPolicyPodSelector{
				NamespaceSelector: &pb.LabelSelector{},
				PodSelector:       &pb.LabelSelector{},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := convertAWSNetworkPolicyPodSelector(tt.pods)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestConvertUnstructuredToAWSNetworkPolicy_OwnerReferences tests that owner references are properly converted
func TestConvertUnstructuredToAWSNetworkPolicy_OwnerReferences(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "networking.k8s.aws/v1alpha1",
			"kind":       "ClusterNetworkPolicy",
			"metadata": map[string]any{
				"name": "test-cnp",
				"ownerReferences": []any{
					map[string]any{
						"apiVersion":         "apps/v1",
						"kind":               "Deployment",
						"name":               "my-deployment",
						"uid":                "owner-uid-123",
						"controller":         true,
						"blockOwnerDeletion": true,
					},
				},
			},
			"spec": map[string]any{
				"priority": int64(10),
				"tier":     "Admin",
			},
		},
	}

	// Set owner references properly using metav1
	obj.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion:         "apps/v1",
			Kind:               "Deployment",
			Name:               "my-deployment",
			UID:                "owner-uid-123",
			Controller:         ptrBool(true),
			BlockOwnerDeletion: ptrBool(true),
		},
	})

	result, err := ConvertUnstructuredToAWSNetworkPolicy(obj)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.GetOwnerReferences(), 1)
	assert.Equal(t, "apps/v1", result.GetOwnerReferences()[0].GetApiVersion())
	assert.Equal(t, "Deployment", result.GetOwnerReferences()[0].GetKind())
	assert.Equal(t, "my-deployment", result.GetOwnerReferences()[0].GetName())
	assert.Equal(t, "owner-uid-123", result.GetOwnerReferences()[0].GetUid())
	assert.True(t, result.GetOwnerReferences()[0].GetController())
	assert.True(t, result.GetOwnerReferences()[0].GetBlockOwnerDeletion())
}
