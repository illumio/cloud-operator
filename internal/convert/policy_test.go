// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

func TestConvertToApplyObject_CiliumNetworkPolicy(t *testing.T) {
	boolTrue := true
	desc := "allow ingress"

	data := &pb.ConfiguredKubernetesObjectData{
		Id:          "cnp-1",
		Name:        "test-policy",
		Namespace:   new("default"),
		Labels:      map[string]string{"env": "prod"},
		Annotations: map[string]string{"note": "test"},
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{
						Description: &desc,
						EndpointSelector: &pb.LabelSelector{
							MatchLabels: map[string]string{"app": "web"},
						},
						EnableDefaultDeny: &pb.CiliumPolicyDefaultDeny{
							Ingress: &boolTrue,
						},
						Ingress: []*pb.CiliumPolicyIngressRule{
							{FromCidr: []string{"10.0.0.0/8"}},
						},
					},
				},
			},
		},
	}

	obj, resourceName, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	assert.Equal(t, "ciliumnetworkpolicies", resourceName)
	assert.Equal(t, "CiliumNetworkPolicy", obj.GetKind())
	assert.Equal(t, "cilium.io/v2", obj.GetAPIVersion())
	assert.Equal(t, "test-policy", obj.GetName())
	assert.Equal(t, "default", obj.GetNamespace())

	metadata, ok := obj.Object["metadata"].(map[string]any)
	require.True(t, ok)
	labels, ok := metadata["labels"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "prod", labels["env"])
	assert.Equal(t, "cnp-1", labels[CloudSecureIDLabel])
	assert.Equal(t, "cloud-operator", labels[ManagedByLabel])

	annotations, ok := metadata["annotations"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "test", annotations["note"])

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok, "expected spec to be a map")
	assert.Contains(t, spec, "endpointSelector")
	assert.Contains(t, spec, "enableDefaultDeny")
	assert.Contains(t, spec, "ingress")
	assert.Equal(t, "allow ingress", spec["description"])
}

func TestConvertToApplyObject_MultipleSpecs(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:   "cnp-multi",
		Name: "multi-spec-policy",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{EndpointSelector: &pb.LabelSelector{MatchLabels: map[string]string{"app": "web"}}},
					{EndpointSelector: &pb.LabelSelector{MatchLabels: map[string]string{"app": "api"}}},
				},
			},
		},
	}

	obj, _, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	_, hasSpec := obj.Object["spec"]
	assert.False(t, hasSpec)

	specs, hasSpecs := obj.Object["specs"]
	assert.True(t, hasSpecs)

	specsList, ok := specs.([]any)
	require.True(t, ok)
	assert.Len(t, specsList, 2)
}

func TestConvertToApplyObject_EmptySpecs(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:   "cnp-empty",
		Name: "empty-spec-policy",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{Specs: []*pb.CiliumPolicyRule{}},
		},
	}

	obj, _, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	_, hasSpec := obj.Object["spec"]
	_, hasSpecs := obj.Object["specs"]

	assert.False(t, hasSpec)
	assert.False(t, hasSpecs)
}

func TestConvertToApplyObject_CiliumClusterwideNetworkPolicy(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:   "ccnp-1",
		Name: "clusterwide-policy",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
			CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{NodeSelector: &pb.LabelSelector{MatchLabels: map[string]string{"role": "worker"}}},
				},
			},
		},
	}

	obj, resourceName, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	assert.Equal(t, "ciliumclusterwidenetworkpolicies", resourceName)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())
	assert.Empty(t, obj.GetNamespace())

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, spec, "nodeSelector")
}

func TestConvertToApplyObject_CiliumCIDRGroup(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:   "cidr-1",
		Name: "test-cidr-group",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumCidrGroup{
			CiliumCidrGroup: &pb.KubernetesCiliumCIDRGroupData{
				Spec: &pb.CiliumCIDRGroup{ExternalCidrs: []string{"10.0.0.0/8", "172.16.0.0/12"}},
			},
		},
	}

	obj, resourceName, err := ConvertToApplyObject(data, "cilium.io", "v2alpha1")
	require.NoError(t, err)

	assert.Equal(t, "ciliumcidrgroups", resourceName)
	assert.Equal(t, "CiliumCIDRGroup", obj.GetKind())

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)

	cidrs, ok := spec["externalCIDRs"].([]any)
	require.True(t, ok)
	assert.Equal(t, "10.0.0.0/8", cidrs[0])
	assert.Equal(t, "172.16.0.0/12", cidrs[1])
}

func TestConvertToApplyObject_NilData(t *testing.T) {
	_, _, err := ConvertToApplyObject(nil, "cilium.io", "v2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestConvertToApplyObject_UnsupportedKindSpecific(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{Id: "unknown-1", Name: "unknown-kind"}
	_, _, err := ConvertToApplyObject(data, "example.io", "v1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")
}

func TestConvertToApplyObject_APIVersionFormats(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:   "cnp-api",
		Name: "api-test",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}

	tests := map[string]struct {
		apiGroup, apiVersion, expectedAPIVer string
	}{
		"with group":         {"cilium.io", "v2", "cilium.io/v2"},
		"core group (empty)": {"", "v1", "v1"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			obj, _, err := ConvertToApplyObject(data, tt.apiGroup, tt.apiVersion)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedAPIVer, obj.GetAPIVersion())
		})
	}
}

func TestConvertToApplyObject_LabelsIncludeManagementLabels(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:     "cnp-labels",
		Name:   "label-test",
		Labels: map[string]string{"custom": "value"},
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}

	obj, _, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	metadata, ok := obj.Object["metadata"].(map[string]any)
	require.True(t, ok)
	labels, ok := metadata["labels"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "value", labels["custom"])
	assert.Equal(t, "cnp-labels", labels[CloudSecureIDLabel])
	assert.Equal(t, "cloud-operator", labels[ManagedByLabel])
	assert.Len(t, labels, 3)
}

func TestConvertToApplyObject_EmptyNamespace(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:   "ccnp-no-ns",
		Name: "cluster-scoped",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
			CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{},
		},
	}

	obj, _, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	metadata, ok := obj.Object["metadata"].(map[string]any)
	require.True(t, ok)

	_, hasNS := metadata["namespace"]
	assert.False(t, hasNS)
}

// From cilium/examples/policies/kubernetes/clusterwide/clusterscope-policy.yaml
// Selective ingress: only pods with name=luke can reach pods with name=leia.
func TestConvertToApplyObject_ClusterwideSelectiveIngress(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:   "ccnp-selective-ingress",
		Name: "selective-ingress",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
			CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{
						EndpointSelector: &pb.LabelSelector{
							MatchLabels: map[string]string{"name": "leia"},
						},
						Ingress: []*pb.CiliumPolicyIngressRule{
							{
								FromEndpoints: &pb.LabelSelectorList{
									Items: []*pb.LabelSelector{
										{MatchLabels: map[string]string{"name": "luke"}},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	obj, resourceName, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	assert.Equal(t, "cilium.io/v2", obj.Object["apiVersion"])
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.Object["kind"])
	assert.Equal(t, "ciliumclusterwidenetworkpolicies", resourceName)

	metadata, ok := obj.Object["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "selective-ingress", metadata["name"])
	_, hasNS := metadata["namespace"]
	assert.False(t, hasNS, "clusterwide policy should have no namespace")

	labels, ok := metadata["labels"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "ccnp-selective-ingress", labels[CloudSecureIDLabel])
	assert.Equal(t, ManagedByValue, labels[ManagedByLabel])

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)

	// endpointSelector matches name=leia
	es, ok := spec["endpointSelector"].(map[string]any)
	require.True(t, ok)
	ml, ok := es["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "leia", ml["name"])

	// ingress fromEndpoints matches name=luke
	ingress, ok := spec["ingress"].([]any)
	require.True(t, ok)
	require.Len(t, ingress, 1)
	rule, ok := ingress[0].(map[string]any)
	require.True(t, ok)

	fromEps, ok := rule["fromEndpoints"].([]any)
	require.True(t, ok, "fromEndpoints should be a flat array, not a wrapper object")
	require.Len(t, fromEps, 1)
	ep, ok := fromEps[0].(map[string]any)
	require.True(t, ok)
	epLabels, ok := ep["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "luke", epLabels["name"])
}

// From cilium/examples/policies/kubernetes/health.yaml
// Health check policy: reserved:health endpoints with ingress/egress remote-node.
func TestConvertToApplyObject_CiliumHealthChecks(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:        "cnp-health",
		Name:      "health",
		Namespace: new("default"),
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{
						EndpointSelector: &pb.LabelSelector{
							MatchLabels: map[string]string{"reserved:health": ""},
						},
						Ingress: []*pb.CiliumPolicyIngressRule{
							{
								FromEntities: []string{"remote-node"},
							},
						},
						Egress: []*pb.CiliumPolicyEgressRule{
							{
								ToEntities: []string{"remote-node"},
							},
						},
					},
				},
			},
		},
	}

	obj, resourceName, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	assert.Equal(t, "cilium.io/v2", obj.Object["apiVersion"])
	assert.Equal(t, "CiliumNetworkPolicy", obj.Object["kind"])
	assert.Equal(t, "ciliumnetworkpolicies", resourceName)

	metadata, ok := obj.Object["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "health", metadata["name"])
	assert.Equal(t, "default", metadata["namespace"])

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)

	// endpointSelector matches reserved:health=""
	es, ok := spec["endpointSelector"].(map[string]any)
	require.True(t, ok)
	ml, ok := es["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, ml["reserved:health"])

	// ingress fromEntities: remote-node
	ingress, ok := spec["ingress"].([]any)
	require.True(t, ok)
	require.Len(t, ingress, 1)
	ingressRule, ok := ingress[0].(map[string]any)
	require.True(t, ok)
	fromEntities, ok := ingressRule["fromEntities"].([]any)
	require.True(t, ok)
	assert.Equal(t, []any{"remote-node"}, fromEntities)

	// egress toEntities: remote-node
	egress, ok := spec["egress"].([]any)
	require.True(t, ok)
	require.Len(t, egress, 1)
	egressRule, ok := egress[0].(map[string]any)
	require.True(t, ok)
	toEntities, ok := egressRule["toEntities"].([]any)
	require.True(t, ok)
	assert.Equal(t, []any{"remote-node"}, toEntities)
}

// From cilium/examples/policies/kubernetes/wildcard/wildcard-from-endpoints.yaml
// DNS ingress: kube-dns selector, empty fromEndpoints (wildcard), toPorts UDP 53.
func TestConvertToApplyObject_WildcardDNSIngress(t *testing.T) {
	udpProto := "UDP"
	data := &pb.ConfiguredKubernetesObjectData{
		Id:        "cnp-wildcard-dns",
		Name:      "wildcard-dns",
		Namespace: new("kube-system"),
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{
						EndpointSelector: &pb.LabelSelector{
							MatchLabels: map[string]string{
								"k8s:io.kubernetes.pod.namespace": "kube-system",
								"k8s-app":                         "kube-dns",
							},
						},
						Ingress: []*pb.CiliumPolicyIngressRule{
							{
								FromEndpoints: &pb.LabelSelectorList{
									Items: []*pb.LabelSelector{
										{}, // empty selector = wildcard
									},
								},
								ToPorts: []*pb.CiliumPolicyPortRule{
									{
										Ports: []*pb.CiliumPolicyPort{
											{Port: "53", Protocol: &udpProto},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	obj, resourceName, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	assert.Equal(t, "CiliumNetworkPolicy", obj.Object["kind"])
	assert.Equal(t, "ciliumnetworkpolicies", resourceName)

	metadata, ok := obj.Object["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "wildcard-dns", metadata["name"])
	assert.Equal(t, "kube-system", metadata["namespace"])

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)

	es, ok := spec["endpointSelector"].(map[string]any)
	require.True(t, ok)
	ml, ok := es["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "kube-system", ml["k8s:io.kubernetes.pod.namespace"])
	assert.Equal(t, "kube-dns", ml["k8s-app"])

	ingress, ok := spec["ingress"].([]any)
	require.True(t, ok)
	require.Len(t, ingress, 1)
	rule, ok := ingress[0].(map[string]any)
	require.True(t, ok)

	// fromEndpoints with empty selector (wildcard)
	fromEps, ok := rule["fromEndpoints"].([]any)
	require.True(t, ok, "fromEndpoints should be a flat array, not a wrapper object")
	require.Len(t, fromEps, 1)
	// empty selector should be an empty map
	ep, ok := fromEps[0].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, ep, "wildcard selector should be empty")

	// toPorts UDP 53
	toPorts, ok := rule["toPorts"].([]any)
	require.True(t, ok)
	require.Len(t, toPorts, 1)
	portRule, ok := toPorts[0].(map[string]any)
	require.True(t, ok)
	ports, ok := portRule["ports"].([]any)
	require.True(t, ok)
	require.Len(t, ports, 1)
	p, ok := ports[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "53", p["port"])
	assert.Equal(t, "UDP", p["protocol"])
}

// From cilium/examples/policies/kubernetes/namespace-labels/namespace-labels-policy.yaml
// Namespace label selectors: faction=alliance.
func TestConvertToApplyObject_NamespaceLabelSelectors(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:        "cnp-ns-labels",
		Name:      "ns-labels-policy",
		Namespace: new("default"),
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{
						EndpointSelector: &pb.LabelSelector{
							MatchLabels: map[string]string{"name": "leia"},
						},
						Ingress: []*pb.CiliumPolicyIngressRule{
							{
								FromEndpoints: &pb.LabelSelectorList{
									Items: []*pb.LabelSelector{
										{
											MatchLabels: map[string]string{
												"name": "luke",
												"k8s:io.cilium.k8s.namespace.labels.faction": "alliance",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	obj, _, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)

	es, ok := spec["endpointSelector"].(map[string]any)
	require.True(t, ok)
	ml, ok := es["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "leia", ml["name"])

	ingress, ok := spec["ingress"].([]any)
	require.True(t, ok)
	require.Len(t, ingress, 1)
	rule, ok := ingress[0].(map[string]any)
	require.True(t, ok)

	fromEps, ok := rule["fromEndpoints"].([]any)
	require.True(t, ok, "fromEndpoints should be a flat array, not a wrapper object")
	require.Len(t, fromEps, 1)
	ep, ok := fromEps[0].(map[string]any)
	require.True(t, ok)
	epLabels, ok := ep["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "luke", epLabels["name"])
	assert.Equal(t, "alliance", epLabels["k8s:io.cilium.k8s.namespace.labels.faction"])
}

// From cilium/examples/policies/kubernetes/kubedns-policy.yaml
// Egress to kube-dns: UDP 53.
func TestConvertToApplyObject_EgressToKubeDNS(t *testing.T) {
	udpProto := "UDP"
	data := &pb.ConfiguredKubernetesObjectData{
		Id:        "cnp-kubedns",
		Name:      "kubedns-policy",
		Namespace: new("default"),
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{
						EndpointSelector: &pb.LabelSelector{},
						Egress: []*pb.CiliumPolicyEgressRule{
							{
								ToEndpoints: &pb.LabelSelectorList{
									Items: []*pb.LabelSelector{
										{
											MatchLabels: map[string]string{
												"k8s:io.kubernetes.pod.namespace": "kube-system",
												"k8s-app":                         "kube-dns",
											},
										},
									},
								},
								ToPorts: []*pb.CiliumPolicyPortRule{
									{
										Ports: []*pb.CiliumPolicyPort{
											{Port: "53", Protocol: &udpProto},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	obj, _, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)

	// endpointSelector should be empty (selects all pods in namespace)
	es, ok := spec["endpointSelector"].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, es, "empty selector should select all pods")

	egress, ok := spec["egress"].([]any)
	require.True(t, ok)
	require.Len(t, egress, 1)
	rule, ok := egress[0].(map[string]any)
	require.True(t, ok)

	// toEndpoints targeting kube-dns
	toEps, ok := rule["toEndpoints"].([]any)
	require.True(t, ok, "toEndpoints should be a flat array, not a wrapper object")
	require.Len(t, toEps, 1)
	ep, ok := toEps[0].(map[string]any)
	require.True(t, ok)
	epLabels, ok := ep["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "kube-system", epLabels["k8s:io.kubernetes.pod.namespace"])
	assert.Equal(t, "kube-dns", epLabels["k8s-app"])

	// toPorts UDP 53
	toPorts, ok := rule["toPorts"].([]any)
	require.True(t, ok)
	require.Len(t, toPorts, 1)
	portRule, ok := toPorts[0].(map[string]any)
	require.True(t, ok)
	ports, ok := portRule["ports"].([]any)
	require.True(t, ok)
	require.Len(t, ports, 1)
	p, ok := ports[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "53", p["port"])
	assert.Equal(t, "UDP", p["protocol"])
}

// From cilium/examples/policies/kubernetes/isolate-namespaces.yaml
// Namespace isolation: empty selectors restrict to same-namespace traffic.
func TestConvertToApplyObject_NamespaceIsolation(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:        "cnp-isolate-ns",
		Name:      "isolate-ns",
		Namespace: new("default"),
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{
						EndpointSelector: &pb.LabelSelector{},
						Ingress: []*pb.CiliumPolicyIngressRule{
							{
								FromEndpoints: &pb.LabelSelectorList{
									Items: []*pb.LabelSelector{
										{}, // empty = same namespace
									},
								},
							},
						},
						Egress: []*pb.CiliumPolicyEgressRule{
							{
								ToEndpoints: &pb.LabelSelectorList{
									Items: []*pb.LabelSelector{
										{}, // empty = same namespace
									},
								},
							},
						},
					},
				},
			},
		},
	}

	obj, resourceName, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	assert.Equal(t, "CiliumNetworkPolicy", obj.Object["kind"])
	assert.Equal(t, "ciliumnetworkpolicies", resourceName)

	metadata, ok := obj.Object["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "isolate-ns", metadata["name"])
	assert.Equal(t, "default", metadata["namespace"])

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)

	// empty endpointSelector = all pods in namespace
	es, ok := spec["endpointSelector"].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, es)

	// ingress: fromEndpoints with empty selector
	ingress, ok := spec["ingress"].([]any)
	require.True(t, ok)
	require.Len(t, ingress, 1)
	ingressRule, ok := ingress[0].(map[string]any)
	require.True(t, ok)
	fromEps, ok := ingressRule["fromEndpoints"].([]any)
	require.True(t, ok, "fromEndpoints should be a flat array, not a wrapper object")
	require.Len(t, fromEps, 1)
	fromEp, ok := fromEps[0].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, fromEp, "empty selector = same namespace")

	// egress: toEndpoints with empty selector
	egress, ok := spec["egress"].([]any)
	require.True(t, ok)
	require.Len(t, egress, 1)
	egressRule, ok := egress[0].(map[string]any)
	require.True(t, ok)
	toEps, ok := egressRule["toEndpoints"].([]any)
	require.True(t, ok, "toEndpoints should be a flat array, not a wrapper object")
	require.Len(t, toEps, 1)
	toEp, ok := toEps[0].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, toEp, "empty selector = same namespace")
}

// Verify that nil annotations marshal as "annotations":null in the JSON sent to the API server,
// so SSA reclaims ownership and clears any annotations not set by CloudSecure.
func TestConvertToApplyObject_NilAnnotationsMarshalAsNull(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:   "cnp-no-annotations",
		Name: "no-annotations",
		// Annotations intentionally not set (nil)
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{EndpointSelector: &pb.LabelSelector{MatchLabels: map[string]string{"app": "test"}}},
				},
			},
		},
	}

	obj, _, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	metadata, ok := obj.Object["metadata"].(map[string]any)
	require.True(t, ok)

	// annotations key must be present in the map (not omitted)
	val, exists := metadata["annotations"]
	assert.True(t, exists, "annotations key must be present so SSA claims ownership")
	assert.Nil(t, val, "nil annotations should remain nil, not an empty map")
}

func TestConvertToApplyObject_EmitUnpopulatedFalse(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:   "cnp-sparse",
		Name: "sparse-policy",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{EndpointSelector: &pb.LabelSelector{MatchLabels: map[string]string{"app": "minimal"}}},
				},
			},
		},
	}

	obj, _, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, spec, "endpointSelector")
	assert.NotContains(t, spec, "description")
	assert.NotContains(t, spec, "nodeSelector")
	assert.NotContains(t, spec, "enableDefaultDeny")
	assert.NotContains(t, spec, "ingress")
	assert.NotContains(t, spec, "egress")
}

func TestCopyLabels(t *testing.T) {
	tests := map[string]struct {
		labels   map[string]string
		id       string
		expected map[string]string
	}{
		"with existing labels": {
			labels: map[string]string{"env": "prod", "team": "platform"},
			id:     "obj-1",
			expected: map[string]string{
				"env": "prod", "team": "platform",
				CloudSecureIDLabel: "obj-1", ManagedByLabel: ManagedByValue,
			},
		},
		"nil labels": {
			labels:   nil,
			id:       "obj-2",
			expected: map[string]string{CloudSecureIDLabel: "obj-2", ManagedByLabel: ManagedByValue},
		},
		"empty labels": {
			labels:   map[string]string{},
			id:       "obj-3",
			expected: map[string]string{CloudSecureIDLabel: "obj-3", ManagedByLabel: ManagedByValue},
		},
		"conflicting management labels are overwritten": {
			labels:   map[string]string{CloudSecureIDLabel: "attacker", ManagedByLabel: "someone-else"},
			id:       "obj-4",
			expected: map[string]string{CloudSecureIDLabel: "obj-4", ManagedByLabel: ManagedByValue},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := copyLabels(tt.labels, tt.id)
			assert.Equal(t, tt.expected, result)
		})
	}
}
