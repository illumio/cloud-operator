// Copyright 2026 Illumio, Inc. All Rights Reserved.

package ovn

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestIsEgressResource(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"EgressFirewall kind", "EgressFirewall", true},
		{"EgressIP kind", "EgressIP", true},
		{"egressfirewalls plural", "egressfirewalls", true},
		{"egressips plural", "egressips", true},
		{"unrelated kind", "NetworkPolicy", false},
		{"unrelated plural", "pods", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsEgressResource(tt.input))
		})
	}
}

func TestConvertUnstructuredToEgressResource_Nil(t *testing.T) {
	result, err := ConvertUnstructuredToEgressResource(nil)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "cannot convert nil object")
}

func TestConvertUnstructuredToEgressResource_UnsupportedKind(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "k8s.ovn.org/v1",
			"kind":       "UnknownEgress",
			"metadata": map[string]any{
				"name": "test",
			},
		},
	}

	result, err := ConvertUnstructuredToEgressResource(obj)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "unsupported Egress resource kind")
}

func TestConvertUnstructuredToEgressResource_EgressFirewall(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "k8s.ovn.org/v1",
			"kind":       "EgressFirewall",
			"metadata": map[string]any{
				"name":            "default",
				"namespace":       "team-a",
				"uid":             "ef-uid",
				"resourceVersion": "42",
				"labels":          map[string]any{"app": "test"},
			},
			"spec": map[string]any{
				"egress": []any{
					map[string]any{
						"type": "Allow",
						"to": map[string]any{
							"cidrSelector": "10.0.0.0/8",
						},
						"ports": []any{
							map[string]any{
								"protocol": "TCP",
								"port":     int64(443),
							},
						},
					},
					map[string]any{
						"type": "Deny",
						"to": map[string]any{
							"dnsName": "www.example.com",
						},
					},
				},
			},
		},
	}

	result, err := ConvertUnstructuredToEgressResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "EgressFirewall", result.GetKind())
	assert.Equal(t, "k8s.ovn.org", result.GetApiGroup())
	assert.Equal(t, "v1", result.GetApiVersion())
	assert.Equal(t, "default", result.GetName())
	assert.Equal(t, "team-a", result.GetNamespace())
	assert.Equal(t, "ef-uid", result.GetUid())

	efData := result.GetEgressFirewall()
	require.NotNil(t, efData)
	require.Len(t, efData.GetEgress(), 2)

	// First rule: Allow to CIDR with port.
	allow := efData.GetEgress()[0]
	assert.Equal(t, "Allow", allow.GetType())
	require.NotNil(t, allow.GetTo())
	assert.Equal(t, "10.0.0.0/8", allow.GetTo().GetCidrSelector())
	assert.Empty(t, allow.GetTo().GetDnsName())
	require.Len(t, allow.GetPorts(), 1)
	assert.Equal(t, "TCP", allow.GetPorts()[0].GetProtocol())
	assert.Equal(t, int32(443), allow.GetPorts()[0].GetPort())

	// Second rule: Deny to DNS name, no ports.
	deny := efData.GetEgress()[1]
	assert.Equal(t, "Deny", deny.GetType())
	require.NotNil(t, deny.GetTo())
	assert.Equal(t, "www.example.com", deny.GetTo().GetDnsName())
	assert.Empty(t, deny.GetTo().GetCidrSelector())
	assert.Empty(t, deny.GetPorts())
}

func TestConvertUnstructuredToEgressResource_EgressFirewall_Empty(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "k8s.ovn.org/v1",
			"kind":       "EgressFirewall",
			"metadata": map[string]any{
				"name":      "default",
				"namespace": "team-b",
			},
			"spec": map[string]any{},
		},
	}

	result, err := ConvertUnstructuredToEgressResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	efData := result.GetEgressFirewall()
	require.NotNil(t, efData)
	assert.Empty(t, efData.GetEgress())
}

func TestConvertUnstructuredToEgressResource_EgressIP(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "k8s.ovn.org/v1",
			"kind":       "EgressIP",
			"metadata": map[string]any{
				"name":            "egressip-prod",
				"uid":             "eip-uid",
				"resourceVersion": "7",
			},
			"spec": map[string]any{
				"egressIPs": []any{"192.168.1.100", "192.168.1.101"},
				"namespaceSelector": map[string]any{
					"matchLabels": map[string]any{"env": "prod"},
				},
				"podSelector": map[string]any{
					"matchLabels": map[string]any{"app": "web"},
				},
			},
		},
	}

	result, err := ConvertUnstructuredToEgressResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "EgressIP", result.GetKind())
	assert.Equal(t, "k8s.ovn.org", result.GetApiGroup())
	assert.Equal(t, "egressip-prod", result.GetName())
	// EgressIP is cluster-scoped: no namespace.
	assert.Empty(t, result.GetNamespace())

	eipData := result.GetEgressIp()
	require.NotNil(t, eipData)
	assert.Equal(t, []string{"192.168.1.100", "192.168.1.101"}, eipData.GetEgressIps())
	require.NotNil(t, eipData.GetNamespaceSelector())
	assert.Equal(t, "prod", eipData.GetNamespaceSelector().GetMatchLabels()["env"])
	require.NotNil(t, eipData.GetPodSelector())
	assert.Equal(t, "web", eipData.GetPodSelector().GetMatchLabels()["app"])
}

func TestConvertUnstructuredToEgressResource_EgressIP_SelectorOnly(t *testing.T) {
	// EgressIP with only a namespace selector and no egress IPs or pod selector.
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "k8s.ovn.org/v1",
			"kind":       "EgressIP",
			"metadata": map[string]any{
				"name": "egressip-selector-only",
			},
			"spec": map[string]any{
				"namespaceSelector": map[string]any{
					"matchLabels": map[string]any{"team": "infra"},
				},
			},
		},
	}

	result, err := ConvertUnstructuredToEgressResource(obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	eipData := result.GetEgressIp()
	require.NotNil(t, eipData)
	assert.Empty(t, eipData.GetEgressIps())
	require.NotNil(t, eipData.GetNamespaceSelector())
	assert.Equal(t, "infra", eipData.GetNamespaceSelector().GetMatchLabels()["team"])
	assert.Nil(t, eipData.GetPodSelector())
}
