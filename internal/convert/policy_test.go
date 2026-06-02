// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

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
	assert.Equal(t, "illumio-cloud-operator", labels[ManagedByLabel])

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
	assert.Equal(t, "illumio-cloud-operator", labels[ManagedByLabel])
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

// Verify that nil annotations are omitted from the apply object so SSA releases
// ownership of previously-owned annotation keys without wiping other managers' annotations.
func TestConvertToApplyObject_NilAnnotationsOmitted(t *testing.T) {
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

	_, exists := metadata["annotations"]
	assert.False(t, exists, "nil annotations should be omitted, not included as null")
}

func TestConvertToApplyObject_WithAnnotations(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:          "cnp-with-annotations",
		Name:        "with-annotations",
		Annotations: map[string]string{"note": "test", "env": "prod"},
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

	annotations, ok := metadata["annotations"].(map[string]string)
	require.True(t, ok, "annotations should be present when set")
	assert.Equal(t, "test", annotations["note"])
	assert.Equal(t, "prod", annotations["env"])
	assert.Len(t, annotations, 2, "only cloud-operator's annotation keys should be present")
}

// Verify that annotations from one apply don't leak into another — each call
// only includes the keys explicitly set by CloudSecure, so SSA never touches
// keys owned by other field managers.
func TestConvertToApplyObject_AnnotationsDoNotLeakBetweenApplies(t *testing.T) {
	// First apply: has annotations
	withAnnotations := &pb.ConfiguredKubernetesObjectData{
		Id:          "cnp-1",
		Name:        "policy-1",
		Annotations: map[string]string{"owner": "team-a"},
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{EndpointSelector: &pb.LabelSelector{MatchLabels: map[string]string{"app": "a"}}},
				},
			},
		},
	}

	obj1, _, err := ConvertToApplyObject(withAnnotations, "cilium.io", "v2")
	require.NoError(t, err)

	meta1, ok := obj1.Object["metadata"].(map[string]any)
	require.True(t, ok)
	ann1, ok := meta1["annotations"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "team-a", ann1["owner"])

	// Second apply: no annotations
	withoutAnnotations := &pb.ConfiguredKubernetesObjectData{
		Id:   "cnp-2",
		Name: "policy-2",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{EndpointSelector: &pb.LabelSelector{MatchLabels: map[string]string{"app": "b"}}},
				},
			},
		},
	}

	obj2, _, err := ConvertToApplyObject(withoutAnnotations, "cilium.io", "v2")
	require.NoError(t, err)

	meta2, ok := obj2.Object["metadata"].(map[string]any)
	require.True(t, ok)

	_, exists := meta2["annotations"]
	assert.False(t, exists, "annotations from a previous apply must not leak into a subsequent one")
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

// TestMarshalUnmarshalRoundTrip_ProtoEqual verifies that marshaling a proto to JSON
// and unmarshaling it back produces an identical proto message.
// Uses the same marshaler config as production (camelCase, omit empty).
// Test cases are based on real Cilium example policies from:
// https://github.com/cilium/cilium/tree/main/examples/policies/kubernetes
func TestMarshalUnmarshalRoundTrip_ProtoEqual(t *testing.T) {
	udpProto := "UDP"

	tests := map[string]struct {
		input *pb.CiliumPolicyRule
	}{
		// From cilium/examples/policies/kubernetes/clusterwide/clusterscope-policy.yaml
		// Selective ingress: only pods with name=luke can reach pods with name=leia
		"clusterwide selective ingress (clusterscope-policy.yaml)": {
			input: &pb.CiliumPolicyRule{
				Description: new("Policy for selective ingress allow to a pod from only a pod with given label"),
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
		// From cilium/examples/policies/kubernetes/clusterwide/health.yaml
		// Cilium health checks: allow ingress/egress from/to remote-node for reserved:health endpoints
		"cilium health checks (health.yaml)": {
			input: &pb.CiliumPolicyRule{
				EndpointSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{"reserved:health": ""},
				},
				Ingress: []*pb.CiliumPolicyIngressRule{
					{FromEntities: []string{"remote-node"}},
				},
				Egress: []*pb.CiliumPolicyEgressRule{
					{ToEntities: []string{"remote-node"}},
				},
			},
		},
		// From cilium/examples/policies/kubernetes/clusterwide/wildcard-from-endpoints.yaml
		// Allow DNS from all managed endpoints to kube-dns
		"wildcard DNS ingress (wildcard-from-endpoints.yaml)": {
			input: &pb.CiliumPolicyRule{
				Description: new("Policy for ingress allow to kube-dns from all Cilium managed endpoints in the cluster"),
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
								{MatchLabels: map[string]string{}},
							},
						},
						ToPorts: []*pb.CiliumPolicyPortRule{
							{Ports: []*pb.CiliumPolicyPort{{Port: "53", Protocol: &udpProto}}},
						},
					},
				},
			},
		},
		// From cilium/examples/policies/kubernetes/namespace/namespace-policy.yaml
		// Cross-namespace ingress: allow from ns2/luke to ns1/leia
		"cross-namespace ingress (namespace-policy.yaml)": {
			input: &pb.CiliumPolicyRule{
				EndpointSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{"name": "leia"},
				},
				Ingress: []*pb.CiliumPolicyIngressRule{
					{
						FromEndpoints: &pb.LabelSelectorList{
							Items: []*pb.LabelSelector{
								{MatchLabels: map[string]string{
									"k8s:io.kubernetes.pod.namespace": "ns2",
									"name":                            "luke",
								}},
							},
						},
					},
				},
			},
		},
		// From cilium/examples/policies/kubernetes/namespace/namespace-labels-policy.yaml
		// Namespace label selectors: allow ingress/egress only from faction=alliance namespaces
		"namespace label selectors (namespace-labels-policy.yaml)": {
			input: &pb.CiliumPolicyRule{
				EndpointSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{"name": "rebel-base"},
				},
				Ingress: []*pb.CiliumPolicyIngressRule{
					{
						FromEndpoints: &pb.LabelSelectorList{
							Items: []*pb.LabelSelector{
								{MatchLabels: map[string]string{"io.cilium.k8s.namespace.labels.faction": "alliance"}},
							},
						},
					},
				},
				Egress: []*pb.CiliumPolicyEgressRule{
					{
						ToEndpoints: &pb.LabelSelectorList{
							Items: []*pb.LabelSelector{
								{MatchLabels: map[string]string{"io.cilium.k8s.namespace.labels.faction": "alliance"}},
							},
						},
					},
				},
			},
		},
		// From cilium/examples/policies/kubernetes/namespace/kubedns-policy.yaml
		// Egress to kube-dns on UDP 53 from all pods in namespace
		"egress to kube-dns (kubedns-policy.yaml)": {
			input: &pb.CiliumPolicyRule{
				EndpointSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{},
				},
				Egress: []*pb.CiliumPolicyEgressRule{
					{
						ToEndpoints: &pb.LabelSelectorList{
							Items: []*pb.LabelSelector{
								{MatchLabels: map[string]string{
									"k8s:io.kubernetes.pod.namespace": "kube-system",
									"k8s-app":                         "kube-dns",
								}},
							},
						},
						ToPorts: []*pb.CiliumPolicyPortRule{
							{Ports: []*pb.CiliumPolicyPort{{Port: "53", Protocol: &udpProto}}},
						},
					},
				},
			},
		},
		// From cilium/examples/policies/kubernetes/namespace/isolate-namespaces.yaml
		// Namespace isolation: only allow ingress from within the same namespace (empty selector = same namespace)
		"namespace isolation (isolate-namespaces.yaml)": {
			input: &pb.CiliumPolicyRule{
				EndpointSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{},
				},
				Ingress: []*pb.CiliumPolicyIngressRule{
					{
						FromEndpoints: &pb.LabelSelectorList{
							Items: []*pb.LabelSelector{
								{MatchLabels: map[string]string{}},
							},
						},
					},
				},
			},
		},
		// Empty rule: only endpointSelector, all optional fields nil
		"nil optional fields": {
			input: &pb.CiliumPolicyRule{
				EndpointSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{"app": "minimal"},
				},
			},
		},
		// Empty slices: present but zero-length (ingress: [], egress: [])
		"empty slices": {
			input: &pb.CiliumPolicyRule{
				EndpointSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{"app": "empty-slices"},
				},
				Ingress:     []*pb.CiliumPolicyIngressRule{},
				Egress:      []*pb.CiliumPolicyEgressRule{},
				IngressDeny: []*pb.CiliumPolicyIngressRule{},
				EgressDeny:  []*pb.CiliumPolicyEgressRule{},
			},
		},
		// Empty maps: present but zero-length
		"empty maps": {
			input: &pb.CiliumPolicyRule{
				EndpointSelector: &pb.LabelSelector{
					MatchLabels:      map[string]string{},
					MatchExpressions: []*pb.LabelSelectorRequirement{},
				},
				Labels: map[string]string{},
			},
		},
		// Empty nested messages: structs present but all their fields are zero/nil
		"empty nested messages": {
			input: &pb.CiliumPolicyRule{
				EndpointSelector:  &pb.LabelSelector{},
				EnableDefaultDeny: &pb.CiliumPolicyDefaultDeny{},
				Description:       new(""),
			},
		},
		// Empty items inside populated slices
		"empty items in slices": {
			input: &pb.CiliumPolicyRule{
				EndpointSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{"app": "empty-items"},
				},
				Ingress: []*pb.CiliumPolicyIngressRule{
					{
						FromEndpoints: &pb.LabelSelectorList{Items: []*pb.LabelSelector{}},
						FromCidr:      []string{},
						FromCidrSet:   []*pb.CiliumPolicyCIDRSet{},
						FromEntities:  []string{},
						ToPorts:       []*pb.CiliumPolicyPortRule{},
					},
				},
				Egress: []*pb.CiliumPolicyEgressRule{
					{
						ToEndpoints: &pb.LabelSelectorList{Items: []*pb.LabelSelector{}},
						ToCidr:      []string{},
						ToCidrSet:   []*pb.CiliumPolicyCIDRSet{},
						ToEntities:  []string{},
						ToFqdns:     []*pb.CiliumPolicyFQDNSelector{},
						ToServices:  []*pb.CiliumPolicyService{},
						ToPorts:     []*pb.CiliumPolicyPortRule{},
					},
				},
			},
		},
	}

	// Same config as protoJSONMarshaler in cilium.go
	marshaler := protojson.MarshalOptions{UseProtoNames: false, EmitUnpopulated: false}

	for testName, tt := range tests {
		t.Run(testName, func(t *testing.T) {
			// Marshal proto → JSON
			jsonBytes, err := marshaler.Marshal(tt.input)
			require.NoError(t, err)

			// Unmarshal JSON → proto
			roundTripped := &pb.CiliumPolicyRule{}
			err = protojson.Unmarshal(jsonBytes, roundTripped)
			require.NoError(t, err)

			// Compare: original and round-tripped should be identical
			assert.True(t, proto.Equal(tt.input, roundTripped),
				"proto not equal after round-trip.\nOriginal JSON:     %s\nRound-tripped: %s",
				jsonBytes,
				func() []byte {
					b, _ := marshaler.Marshal(roundTripped)

					return b
				}(),
			)
		})
	}
}

// TestMarshalProducesCamelCase verifies the production marshaler outputs camelCase keys.
// If UseProtoNames were accidentally set to true, the output would have snake_case keys
// (e.g. "from_endpoints" instead of "fromEndpoints"), which Cilium's CRDs would silently
// ignore — leaving those fields empty in the applied policy.
func TestMarshalProducesCamelCase(t *testing.T) {
	udpProto := "UDP"

	// From cilium/examples/policies/kubernetes/clusterwide/wildcard-from-endpoints.yaml
	rule := &pb.CiliumPolicyRule{
		Description: new("Policy for ingress allow to kube-dns from all Cilium managed endpoints in the cluster"),
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
						{MatchLabels: map[string]string{}},
					},
				},
				ToPorts: []*pb.CiliumPolicyPortRule{
					{Ports: []*pb.CiliumPolicyPort{{Port: "53", Protocol: &udpProto}}},
				},
			},
		},
	}

	// Production marshaler (camelCase)
	camelJSON, err := protoJSONMarshaler.Marshal(rule)
	require.NoError(t, err)

	camelStr := string(camelJSON)

	// Verify camelCase keys are present
	assert.Contains(t, camelStr, "endpointSelector")
	assert.Contains(t, camelStr, "fromEndpoints")
	assert.Contains(t, camelStr, "toPorts")
	assert.Contains(t, camelStr, "matchLabels")

	// Verify snake_case keys are NOT present — if they were, Cilium would silently drop them
	assert.NotContains(t, camelStr, "endpoint_selector")
	assert.NotContains(t, camelStr, "from_endpoints")
	assert.NotContains(t, camelStr, "to_ports")
	assert.NotContains(t, camelStr, "match_labels")

	// Show what would happen with snake_case: same data, wrong key names
	snakeJSON, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(rule)
	require.NoError(t, err)

	snakeStr := string(snakeJSON)

	// Snake_case produces keys that Cilium wouldn't recognize
	assert.Contains(t, snakeStr, "endpoint_selector")
	assert.Contains(t, snakeStr, "from_endpoints")
	assert.Contains(t, snakeStr, "to_ports")
	assert.Contains(t, snakeStr, "match_labels")
	assert.NotContains(t, snakeStr, "endpointSelector")
	assert.NotContains(t, snakeStr, "fromEndpoints")
	assert.NotContains(t, snakeStr, "toPorts")
}

// TestMarshalUnmarshalRoundTrip_ViaMap verifies the full ConvertToApplyObject path:
// proto → protojson → json.Unmarshal(map[string]any) → json.Marshal → protojson.Unmarshal → proto.
// This is the actual code path used in reconciliation (proto → map for SSA apply).
// Test cases are based on real Cilium example policies from:
// https://github.com/cilium/cilium/tree/main/examples/policies/kubernetes
func TestMarshalUnmarshalRoundTrip_ViaMap(t *testing.T) {
	udpProto := "UDP"

	tests := map[string]struct {
		input *pb.CiliumPolicyRule
	}{
		// From cilium/examples/policies/kubernetes/clusterwide/clusterscope-policy.yaml
		"clusterwide selective ingress via map": {
			input: &pb.CiliumPolicyRule{
				Description: new("Policy for selective ingress allow to a pod from only a pod with given label"),
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
		// From cilium/examples/policies/kubernetes/clusterwide/health.yaml
		"cilium health checks via map": {
			input: &pb.CiliumPolicyRule{
				EndpointSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{"reserved:health": ""},
				},
				Ingress: []*pb.CiliumPolicyIngressRule{
					{FromEntities: []string{"remote-node"}},
				},
				Egress: []*pb.CiliumPolicyEgressRule{
					{ToEntities: []string{"remote-node"}},
				},
			},
		},
		// From cilium/examples/policies/kubernetes/namespace/kubedns-policy.yaml
		"egress to kube-dns via map": {
			input: &pb.CiliumPolicyRule{
				EndpointSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{},
				},
				Egress: []*pb.CiliumPolicyEgressRule{
					{
						ToEndpoints: &pb.LabelSelectorList{
							Items: []*pb.LabelSelector{
								{MatchLabels: map[string]string{
									"k8s:io.kubernetes.pod.namespace": "kube-system",
									"k8s-app":                         "kube-dns",
								}},
							},
						},
						ToPorts: []*pb.CiliumPolicyPortRule{
							{Ports: []*pb.CiliumPolicyPort{{Port: "53", Protocol: &udpProto}}},
						},
					},
				},
			},
		},
		// From cilium/examples/policies/kubernetes/namespace/isolate-namespaces.yaml
		"namespace isolation via map": {
			input: &pb.CiliumPolicyRule{
				EndpointSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{},
				},
				Ingress: []*pb.CiliumPolicyIngressRule{
					{
						FromEndpoints: &pb.LabelSelectorList{
							Items: []*pb.LabelSelector{
								{MatchLabels: map[string]string{}},
							},
						},
					},
				},
			},
		},
		// Empty rule
		"empty rule via map": {
			input: &pb.CiliumPolicyRule{
				EndpointSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{"app": "empty"},
				},
			},
		},
	}

	for testName, tt := range tests {
		t.Run(testName, func(t *testing.T) {
			// Step 1: proto → JSON (camelCase, same as protoJSONMarshaler)
			marshaler := protojson.MarshalOptions{UseProtoNames: false, EmitUnpopulated: false}
			jsonBytes, err := marshaler.Marshal(tt.input)
			require.NoError(t, err)

			// Step 2: JSON → map[string]any (what ConvertToApplyObject does internally)
			var intermediate map[string]any

			err = json.Unmarshal(jsonBytes, &intermediate)
			require.NoError(t, err)

			// Step 3: map[string]any → JSON (re-serialize the map)
			reserializedJSON, err := json.Marshal(intermediate)
			require.NoError(t, err)

			// Step 4: JSON → proto (unmarshal back)
			roundTripped := &pb.CiliumPolicyRule{}
			err = protojson.Unmarshal(reserializedJSON, roundTripped)
			require.NoError(t, err)

			// The round-tripped proto should equal the original
			assert.True(t, proto.Equal(tt.input, roundTripped),
				"proto not equal after map round-trip.\nOriginal JSON:      %s\nReserialized JSON:  %s",
				string(jsonBytes),
				string(reserializedJSON),
			)
		})
	}
}

func TestNormalizeProtojsonForCilium_UnwrapsFromEndpoints(t *testing.T) {
	specMap := map[string]any{
		"ingress": []any{
			map[string]any{
				"fromEndpoints": map[string]any{
					"items": []any{
						map[string]any{"matchLabels": map[string]any{"name": "luke"}},
					},
				},
			},
		},
	}

	normalizeProtojsonForCilium(specMap)

	ingress, ok := specMap["ingress"].([]any)
	require.True(t, ok)
	rule, ok := ingress[0].(map[string]any)
	require.True(t, ok)
	fromEps, ok := rule["fromEndpoints"].([]any)
	require.True(t, ok, "fromEndpoints should be a flat array after normalization")
	require.Len(t, fromEps, 1)
	ep, ok := fromEps[0].(map[string]any)
	require.True(t, ok)
	labels, ok := ep["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "luke", labels["name"])
}

func TestNormalizeProtojsonForCilium_UnwrapsToEndpoints(t *testing.T) {
	specMap := map[string]any{
		"egress": []any{
			map[string]any{
				"toEndpoints": map[string]any{
					"items": []any{
						map[string]any{"matchLabels": map[string]any{"app": "dns"}},
					},
				},
			},
		},
	}

	normalizeProtojsonForCilium(specMap)

	egress, ok := specMap["egress"].([]any)
	require.True(t, ok)
	rule, ok := egress[0].(map[string]any)
	require.True(t, ok)
	toEps, ok := rule["toEndpoints"].([]any)
	require.True(t, ok, "toEndpoints should be a flat array after normalization")
	require.Len(t, toEps, 1)
	ep, ok := toEps[0].(map[string]any)
	require.True(t, ok)
	labels, ok := ep["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "dns", labels["app"])
}

func TestNormalizeProtojsonForCilium_UnwrapsDenyRules(t *testing.T) {
	specMap := map[string]any{
		"ingressDeny": []any{
			map[string]any{
				"fromEndpoints": map[string]any{
					"items": []any{map[string]any{}},
				},
			},
		},
		"egressDeny": []any{
			map[string]any{
				"toEndpoints": map[string]any{
					"items": []any{map[string]any{}},
				},
			},
		},
	}

	normalizeProtojsonForCilium(specMap)

	ingressDeny, ok := specMap["ingressDeny"].([]any)
	require.True(t, ok)
	ingressRule, ok := ingressDeny[0].(map[string]any)
	require.True(t, ok)
	_, ok = ingressRule["fromEndpoints"].([]any)
	require.True(t, ok, "fromEndpoints in ingressDeny should be unwrapped")

	egressDeny, ok := specMap["egressDeny"].([]any)
	require.True(t, ok)
	egressRule, ok := egressDeny[0].(map[string]any)
	require.True(t, ok)
	_, ok = egressRule["toEndpoints"].([]any)
	require.True(t, ok, "toEndpoints in egressDeny should be unwrapped")
}

func TestNormalizeProtojsonForCilium_NoEndpoints(t *testing.T) {
	specMap := map[string]any{
		"ingress": []any{
			map[string]any{
				"toPorts": []any{map[string]any{"port": "80"}},
			},
		},
	}

	normalizeProtojsonForCilium(specMap)

	ingress, ok := specMap["ingress"].([]any)
	require.True(t, ok)
	rule, ok := ingress[0].(map[string]any)
	require.True(t, ok)
	assert.Nil(t, rule["fromEndpoints"], "should not add fromEndpoints when absent")
}

func TestNormalizeProtojsonForCilium_EmptyEndpointsWrapper(t *testing.T) {
	// When LabelSelectorList exists but items is empty/nil, protojson emits
	// {"fromEndpoints": {}} — an empty map. Cilium expects an empty array [].
	specMap := map[string]any{
		"ingress": []any{
			map[string]any{
				"fromEndpoints": map[string]any{},
			},
		},
		"egress": []any{
			map[string]any{
				"toEndpoints": map[string]any{},
			},
		},
	}

	normalizeProtojsonForCilium(specMap)

	ingress, ok := specMap["ingress"].([]any)
	require.True(t, ok)
	ingressRule, ok := ingress[0].(map[string]any)
	require.True(t, ok)
	fromEps, ok := ingressRule["fromEndpoints"].([]any)
	require.True(t, ok, "empty wrapper should become empty array, not remain a map")
	assert.Empty(t, fromEps)

	egress, ok := specMap["egress"].([]any)
	require.True(t, ok)
	egressRule, ok := egress[0].(map[string]any)
	require.True(t, ok)
	toEps, ok := egressRule["toEndpoints"].([]any)
	require.True(t, ok, "empty wrapper should become empty array, not remain a map")
	assert.Empty(t, toEps)
}

func TestNormalizeProtojsonForCilium_ICMPTypeInt(t *testing.T) {
	specMap := map[string]any{
		"ingress": []any{
			map[string]any{
				"icmps": []any{
					map[string]any{
						"fields": []any{
							map[string]any{"family": "IPv4", "typeInt": float64(8)},
						},
					},
				},
			},
		},
	}

	normalizeProtojsonForCilium(specMap)

	ingress, ok := specMap["ingress"].([]any)
	require.True(t, ok)
	rule, ok := ingress[0].(map[string]any)
	require.True(t, ok)
	icmps, ok := rule["icmps"].([]any)
	require.True(t, ok)
	icmpEntry, ok := icmps[0].(map[string]any)
	require.True(t, ok)
	fields, ok := icmpEntry["fields"].([]any)
	require.True(t, ok)
	field, ok := fields[0].(map[string]any)
	require.True(t, ok)

	assert.InDelta(t, float64(8), field["type"], 0, "typeInt should be renamed to type")
	assert.Nil(t, field["typeInt"], "typeInt should be removed")
}

func TestNormalizeProtojsonForCilium_ICMPTypeString(t *testing.T) {
	specMap := map[string]any{
		"egress": []any{
			map[string]any{
				"icmps": []any{
					map[string]any{
						"fields": []any{
							map[string]any{"family": "IPv4", "typeString": "EchoReply"},
						},
					},
				},
			},
		},
	}

	normalizeProtojsonForCilium(specMap)

	egress, ok := specMap["egress"].([]any)
	require.True(t, ok)
	rule, ok := egress[0].(map[string]any)
	require.True(t, ok)
	icmps, ok := rule["icmps"].([]any)
	require.True(t, ok)
	icmpEntry, ok := icmps[0].(map[string]any)
	require.True(t, ok)
	fields, ok := icmpEntry["fields"].([]any)
	require.True(t, ok)
	field, ok := fields[0].(map[string]any)
	require.True(t, ok)

	assert.Equal(t, "EchoReply", field["type"], "typeString should be renamed to type")
	assert.Nil(t, field["typeString"], "typeString should be removed")
}

func TestNormalizeProtojsonForCilium_EndToEnd(t *testing.T) {
	// Verify normalization through the full marshalPolicySpecs path
	udpProto := "UDP"
	specs := []*pb.CiliumPolicyRule{
		{
			EndpointSelector: &pb.LabelSelector{
				MatchLabels: map[string]string{"app": "web"},
			},
			Ingress: []*pb.CiliumPolicyIngressRule{
				{
					FromEndpoints: &pb.LabelSelectorList{
						Items: []*pb.LabelSelector{
							{MatchLabels: map[string]string{"app": "frontend"}},
						},
					},
					ToPorts: []*pb.CiliumPolicyPortRule{
						{Ports: []*pb.CiliumPolicyPort{{Port: "53", Protocol: &udpProto}}},
					},
				},
			},
			Egress: []*pb.CiliumPolicyEgressRule{
				{
					ToEndpoints: &pb.LabelSelectorList{
						Items: []*pb.LabelSelector{
							{MatchLabels: map[string]string{"app": "dns"}},
						},
					},
				},
			},
		},
	}

	result, err := marshalPolicySpecs(specs)
	require.NoError(t, err)

	spec, ok := result["spec"].(map[string]any)
	require.True(t, ok)

	// fromEndpoints must be a flat array
	ingress, ok := spec["ingress"].([]any)
	require.True(t, ok)
	ingressRule, ok := ingress[0].(map[string]any)
	require.True(t, ok)
	fromEps, ok := ingressRule["fromEndpoints"].([]any)
	require.True(t, ok, "fromEndpoints should be a flat array, not a wrapper object")
	require.Len(t, fromEps, 1)
	fromEp, ok := fromEps[0].(map[string]any)
	require.True(t, ok)
	fromLabels, ok := fromEp["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "frontend", fromLabels["app"])

	// toEndpoints must be a flat array
	egress, ok := spec["egress"].([]any)
	require.True(t, ok)
	egressRule, ok := egress[0].(map[string]any)
	require.True(t, ok)
	toEps, ok := egressRule["toEndpoints"].([]any)
	require.True(t, ok, "toEndpoints should be a flat array, not a wrapper object")
	require.Len(t, toEps, 1)
	toEp, ok := toEps[0].(map[string]any)
	require.True(t, ok)
	toLabels, ok := toEp["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "dns", toLabels["app"])
}

// TestMarshalPolicySpecs_EndpointSemantics validates the critical distinction between
// [], [{}], and nil for fromEndpoints/toEndpoints through the full marshalPolicySpecs path.
//
// In Cilium:
//   - fromEndpoints: []   → "allow nobody" (empty whitelist)
//   - fromEndpoints: [{}] → "allow everybody" (wildcard, all pods in namespace)
//   - fromEndpoints: nil  → implicit wildcard (when toPorts is set)
//
// Confusing [] and [{}] reverses the security posture entirely.
func TestMarshalPolicySpecs_EndpointSemantics(t *testing.T) {
	t.Run("empty list means allow nobody", func(t *testing.T) {
		specs := []*pb.CiliumPolicyRule{
			{
				EndpointSelector: &pb.LabelSelector{},
				Ingress: []*pb.CiliumPolicyIngressRule{
					{
						FromEndpoints: &pb.LabelSelectorList{
							Items: []*pb.LabelSelector{},
						},
					},
				},
				Egress: []*pb.CiliumPolicyEgressRule{
					{
						ToEndpoints: &pb.LabelSelectorList{
							Items: []*pb.LabelSelector{},
						},
					},
				},
			},
		}

		result, err := marshalPolicySpecs(specs)
		require.NoError(t, err)

		spec, ok := result["spec"].(map[string]any)
		require.True(t, ok)

		// fromEndpoints must be [] (empty array), NOT [{}]
		ingress, ok := spec["ingress"].([]any)
		require.True(t, ok)
		ingressRule, ok := ingress[0].(map[string]any)
		require.True(t, ok)
		fromEps, ok := ingressRule["fromEndpoints"].([]any)
		require.True(t, ok, "fromEndpoints should be an array")
		assert.Empty(t, fromEps, "fromEndpoints: [] means allow nobody — must be empty array")

		// toEndpoints must be [] (empty array), NOT [{}]
		egress, ok := spec["egress"].([]any)
		require.True(t, ok)
		egressRule, ok := egress[0].(map[string]any)
		require.True(t, ok)
		toEps, ok := egressRule["toEndpoints"].([]any)
		require.True(t, ok, "toEndpoints should be an array")
		assert.Empty(t, toEps, "toEndpoints: [] means allow nobody — must be empty array")
	})

	t.Run("single empty selector means allow everybody", func(t *testing.T) {
		specs := []*pb.CiliumPolicyRule{
			{
				EndpointSelector: &pb.LabelSelector{},
				Ingress: []*pb.CiliumPolicyIngressRule{
					{
						FromEndpoints: &pb.LabelSelectorList{
							Items: []*pb.LabelSelector{{}},
						},
					},
				},
				Egress: []*pb.CiliumPolicyEgressRule{
					{
						ToEndpoints: &pb.LabelSelectorList{
							Items: []*pb.LabelSelector{{}},
						},
					},
				},
			},
		}

		result, err := marshalPolicySpecs(specs)
		require.NoError(t, err)

		spec, ok := result["spec"].(map[string]any)
		require.True(t, ok)

		// fromEndpoints must be [{}] (one empty selector), NOT []
		ingress, ok := spec["ingress"].([]any)
		require.True(t, ok)
		ingressRule, ok := ingress[0].(map[string]any)
		require.True(t, ok)
		fromEps, ok := ingressRule["fromEndpoints"].([]any)
		require.True(t, ok, "fromEndpoints should be an array")
		require.Len(t, fromEps, 1, "fromEndpoints: [{}] means allow everybody — must have one element")
		ep, ok := fromEps[0].(map[string]any)
		require.True(t, ok)
		assert.Empty(t, ep, "the single selector should be empty (wildcard)")

		// toEndpoints must be [{}] (one empty selector), NOT []
		egress, ok := spec["egress"].([]any)
		require.True(t, ok)
		egressRule, ok := egress[0].(map[string]any)
		require.True(t, ok)
		toEps, ok := egressRule["toEndpoints"].([]any)
		require.True(t, ok, "toEndpoints should be an array")
		require.Len(t, toEps, 1, "toEndpoints: [{}] means allow everybody — must have one element")
		toEp, ok := toEps[0].(map[string]any)
		require.True(t, ok)
		assert.Empty(t, toEp, "the single selector should be empty (wildcard)")
	})

	t.Run("nil endpoints omitted for implicit wildcard", func(t *testing.T) {
		udpProto := "UDP"
		specs := []*pb.CiliumPolicyRule{
			{
				EndpointSelector: &pb.LabelSelector{},
				Ingress: []*pb.CiliumPolicyIngressRule{
					{
						FromEndpoints: nil,
						ToPorts: []*pb.CiliumPolicyPortRule{
							{Ports: []*pb.CiliumPolicyPort{{Port: "53", Protocol: &udpProto}}},
						},
					},
				},
			},
		}

		result, err := marshalPolicySpecs(specs)
		require.NoError(t, err)

		spec, ok := result["spec"].(map[string]any)
		require.True(t, ok)
		ingress, ok := spec["ingress"].([]any)
		require.True(t, ok)
		ingressRule, ok := ingress[0].(map[string]any)
		require.True(t, ok)

		_, exists := ingressRule["fromEndpoints"]
		assert.False(t, exists, "nil fromEndpoints should be omitted entirely (implicit wildcard)")
		assert.Contains(t, ingressRule, "toPorts", "toPorts should still be present")
	})
}

// TestMarshalPolicySpecs_JSONNameOverrides verifies that proto fields with json_name overrides
// produce the exact casing Cilium's CRD expects. Without these overrides, protojson would emit
// camelCase (e.g. "fromCidr") instead of the required form (e.g. "fromCIDR"), and Cilium would
// silently ignore the field.
func TestMarshalPolicySpecs_JSONNameOverrides(t *testing.T) {
	t.Run("ingress CIDR fields", func(t *testing.T) {
		cidr := "10.0.0.0/8"
		specs := []*pb.CiliumPolicyRule{
			{
				EndpointSelector: &pb.LabelSelector{},
				Ingress: []*pb.CiliumPolicyIngressRule{
					{
						FromCidr: []string{"10.0.0.0/8"},
						FromCidrSet: []*pb.CiliumPolicyCIDRSet{
							{Cidr: &cidr, Except: []string{"10.96.0.0/12"}},
						},
					},
				},
			},
		}

		result, err := marshalPolicySpecs(specs)
		require.NoError(t, err)

		spec, ok := result["spec"].(map[string]any)
		require.True(t, ok)
		ingress, ok := spec["ingress"].([]any)
		require.True(t, ok)
		rule, ok := ingress[0].(map[string]any)
		require.True(t, ok)

		assert.Contains(t, rule, "fromCIDR", "json_name override: fromCIDR, not fromCidr")
		assert.NotContains(t, rule, "fromCidr")
		assert.Contains(t, rule, "fromCIDRSet", "json_name override: fromCIDRSet, not fromCidrSet")
		assert.NotContains(t, rule, "fromCidrSet")
	})

	t.Run("egress CIDR and FQDN fields", func(t *testing.T) {
		cidr := "10.0.0.0/8"
		specs := []*pb.CiliumPolicyRule{
			{
				EndpointSelector: &pb.LabelSelector{},
				Egress: []*pb.CiliumPolicyEgressRule{
					{
						ToCidr: []string{"10.0.0.0/8"},
						ToCidrSet: []*pb.CiliumPolicyCIDRSet{
							{Cidr: &cidr},
						},
						ToFqdns: []*pb.CiliumPolicyFQDNSelector{
							{MatchName: new("example.com")},
						},
					},
				},
			},
		}

		result, err := marshalPolicySpecs(specs)
		require.NoError(t, err)

		spec, ok := result["spec"].(map[string]any)
		require.True(t, ok)
		egress, ok := spec["egress"].([]any)
		require.True(t, ok)
		rule, ok := egress[0].(map[string]any)
		require.True(t, ok)

		assert.Contains(t, rule, "toCIDR", "json_name override: toCIDR, not toCidr")
		assert.NotContains(t, rule, "toCidr")
		assert.Contains(t, rule, "toCIDRSet", "json_name override: toCIDRSet, not toCidrSet")
		assert.NotContains(t, rule, "toCidrSet")
		assert.Contains(t, rule, "toFQDNs", "json_name override: toFQDNs, not toFqdns")
		assert.NotContains(t, rule, "toFqdns")
	})

	t.Run("CIDRGroup externalCIDRs", func(t *testing.T) {
		data := &pb.ConfiguredKubernetesObjectData{
			Id:   "cidr-json-name",
			Name: "cidr-group-test",
			KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumCidrGroup{
				CiliumCidrGroup: &pb.KubernetesCiliumCIDRGroupData{
					Spec: &pb.CiliumCIDRGroup{ExternalCidrs: []string{"10.0.0.0/8"}},
				},
			},
		}

		obj, _, err := ConvertToApplyObject(data, "cilium.io", "v2alpha1")
		require.NoError(t, err)

		spec, ok := obj.Object["spec"].(map[string]any)
		require.True(t, ok)
		assert.Contains(t, spec, "externalCIDRs", "json_name override: externalCIDRs, not externalCidrs")
		assert.NotContains(t, spec, "externalCidrs")
	})

	t.Run("AWS security group fields", func(t *testing.T) {
		specs := []*pb.CiliumPolicyRule{
			{
				EndpointSelector: &pb.LabelSelector{},
				Egress: []*pb.CiliumPolicyEgressRule{
					{
						ToGroups: []*pb.CiliumPolicyGroup{
							{
								CloudProvider: &pb.CiliumPolicyGroup_Aws{
									Aws: &pb.CiliumPolicyAWSGroup{
										SecurityGroupIds:   []string{"sg-123"},
										SecurityGroupNames: []string{"my-sg"},
										Region:             new("us-east-1"),
									},
								},
							},
						},
					},
				},
			},
		}

		result, err := marshalPolicySpecs(specs)
		require.NoError(t, err)

		spec, ok := result["spec"].(map[string]any)
		require.True(t, ok)
		egress, ok := spec["egress"].([]any)
		require.True(t, ok)
		rule, ok := egress[0].(map[string]any)
		require.True(t, ok)
		toGroups, ok := rule["toGroups"].([]any)
		require.True(t, ok)
		group, ok := toGroups[0].(map[string]any)
		require.True(t, ok)
		aws, ok := group["aws"].(map[string]any)
		require.True(t, ok)

		assert.Contains(t, aws, "securityGroupsIds", "json_name override: securityGroupsIds")
		assert.NotContains(t, aws, "securityGroupIds")
		assert.Contains(t, aws, "securityGroupsNames", "json_name override: securityGroupsNames")
		assert.NotContains(t, aws, "securityGroupNames")
	})
}
