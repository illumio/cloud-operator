// Copyright 2026 Illumio, Inc. All Rights Reserved.

//go:build envtest

package reconciler

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kjson "k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/yaml"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller"
)

// loadExpectedPolicy reads a Cilium policy YAML from testdata/policies/ and returns
// the parsed top-level map for comparison with K8s unstructured objects.
// Uses K8s's JSON unmarshaler (k8s.io/apimachinery/pkg/util/json) which decodes
// integers as int64 — matching how K8s unstructured objects store numbers.
func loadExpectedPolicy(t *testing.T, filename string) map[string]any {
	t.Helper()

	data, err := os.ReadFile("testdata/policies/" + filename)
	require.NoError(t, err)

	jsonData, err := yaml.YAMLToJSON(data)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, kjson.Unmarshal(jsonData, &parsed))

	// protojson omits empty maps, so the reconciler produces
	// endpointSelector: {} rather than endpointSelector: {matchLabels: {}}.
	removeEmptyMatchLabels(parsed)

	return parsed
}

// removeEmptyMatchLabels recursively removes matchLabels keys whose value is
// an empty map, matching protojson's behavior of omitting empty maps.
func removeEmptyMatchLabels(v any) {
	switch val := v.(type) {
	case map[string]any:
		for _, v := range val {
			removeEmptyMatchLabels(v)
		}
		if ml, ok := val["matchLabels"]; ok {
			if m, ok := ml.(map[string]any); ok && len(m) == 0 {
				delete(val, "matchLabels")
			}
		}
	case []any:
		for _, item := range val {
			removeEmptyMatchLabels(item)
		}
	}
}

func TestReconciler_PopulatedSnapshot(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()
	boolTrue := true

	// Push config + policy + snapshot complete through the gRPC stream
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:        "cnp-e2e-1",
				Name:      "e2e-snapshot-policy",

				Labels:    map[string]string{"env": "e2e"},
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "web"},
								},
								EnableDefaultDeny: &pb.CiliumPolicyDefaultDeny{
									Ingress: &boolTrue,
								},
							},
						},
					},
				},
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	// Wait for the policy to appear in envtest
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-snapshot-policy")
		return err == nil && obj != nil
	}, 10*time.Second, 100*time.Millisecond, "policy should be applied to K8s")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-snapshot-policy")
	require.NoError(t, err)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())
	assert.Equal(t, "e2e-snapshot-policy", obj.GetName())
	assert.Equal(t, "e2e", obj.GetLabels()["env"])
	assert.Equal(t, controller.ManagedByValue, obj.GetLabels()[controller.ManagedByLabel])
	assert.Equal(t, "cnp-e2e-1", obj.GetLabels()[controller.CloudSecureIDLabel])

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)

	es, ok := spec["endpointSelector"].(map[string]any)
	require.True(t, ok)
	ml, ok := es["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "web", ml["app"])

	enableDefaultDeny, ok := spec["enableDefaultDeny"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, enableDefaultDeny["ingress"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-snapshot-policy")
	})
}

func TestReconciler_EmptySnapshotThenMutation(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	// Send config + empty snapshot (no policies)
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	// Give reconciler time to process the empty snapshot
	time.Sleep(500 * time.Millisecond)

	// Verify nothing was created
	_, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-mutation-policy")
	require.Error(t, err, "no policy should exist yet")

	// Now send a create mutation
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_CreateObject{
					CreateObject: &pb.ConfiguredKubernetesObjectData{
						Id:        "cnp-e2e-2",
						Name:      "e2e-mutation-policy",
		
						KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
							CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
								Specs: []*pb.CiliumPolicyRule{
									{
										EndpointSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"app": "api"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	})

	// Wait for the policy to appear
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-mutation-policy")
		return err == nil && obj != nil
	}, 10*time.Second, 100*time.Millisecond, "mutation policy should be applied to K8s")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-mutation-policy")
	require.NoError(t, err)
	assert.Equal(t, "e2e-mutation-policy", obj.GetName())
	assert.Equal(t, "cnp-e2e-2", obj.GetLabels()[controller.CloudSecureIDLabel])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-mutation-policy")
	})
}

func TestReconciler_MutationDelete(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	// Send snapshot with one policy
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:        "cnp-e2e-3",
				Name:      "e2e-delete-policy",

				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "temp"},
								},
							},
						},
					},
				},
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	// Wait for policy to appear
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-delete-policy")
		return err == nil && obj != nil
	}, 10*time.Second, 100*time.Millisecond, "policy should be applied before delete")

	// Send delete mutation
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_DeleteObject{
					DeleteObject: &pb.DeleteConfiguredKubernetesObject{
						Id: "cnp-e2e-3",
					},
				},
			},
		},
	})

	// Wait for policy to be deleted
	require.Eventually(t, func() bool {
		_, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-delete-policy")
		return err != nil
	}, 20*time.Second, 100*time.Millisecond, "policy should be deleted from K8s")
}

func TestReconciler_SSAFieldManagerOwnership(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	// Push config + policy + snapshot complete through the gRPC stream
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:        "cnp-ssa",
				Name:      "e2e-ssa-policy",

				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "ssa"},
								},
							},
						},
					},
				},
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	// Wait for the policy to appear
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-ssa-policy")
		return err == nil && obj != nil
	}, 10*time.Second, 100*time.Millisecond, "policy should be applied to K8s")

	// Verify SSA field manager ownership
	obj, err := testClient.GetDynamicClient().Resource(ccnpGVR).Get(ctx, "e2e-ssa-policy", metav1.GetOptions{})
	require.NoError(t, err)

	managedFields := obj.GetManagedFields()
	require.NotEmpty(t, managedFields, "SSA should set managed fields")

	var found bool
	for _, mf := range managedFields {
		if mf.Manager == FieldManager {
			found = true

			break
		}
	}

	assert.True(t, found, "expected field manager %q in managed fields", FieldManager)

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-ssa-policy")
	})
}

func TestReconciler_ClusterwideIngressWithPorts(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	udpProto := "UDP"

	// CiliumClusterwideNetworkPolicy: allow ingress to kube-dns from all endpoints on port 53/UDP
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "ccnp-wildcard",
				Name: "wildcard-from-endpoints",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								Description: strPtr("Allow ingress to kube-dns from all Cilium managed endpoints"),
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
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "wildcard-from-endpoints")
		return err == nil && obj != nil
	}, 10*time.Second, 100*time.Millisecond, "clusterwide policy should be applied to K8s")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "wildcard-from-endpoints")
	require.NoError(t, err)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())
	assert.Equal(t, controller.ManagedByValue, obj.GetLabels()[controller.ManagedByLabel])

	expected := loadExpectedPolicy(t, "wildcard-from-endpoints.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "wildcard-from-endpoints")
	})
}

func TestReconciler_DenyAllEgress(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	// CiliumClusterwideNetworkPolicy: deny all egress from pods with role=restricted
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "cnp-deny-egress",
				Name: "deny-all-egress",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"role": "restricted"},
								},
								Egress: []*pb.CiliumPolicyEgressRule{{}},
							},
						},
					},
				},
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "deny-all-egress")
		return err == nil && obj != nil
	}, 10*time.Second, 100*time.Millisecond, "deny-all-egress policy should be applied to K8s")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "deny-all-egress")
	require.NoError(t, err)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())
	assert.Equal(t, controller.ManagedByValue, obj.GetLabels()[controller.ManagedByLabel])

	expected := loadExpectedPolicy(t, "deny-all-egress.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "deny-all-egress")
	})
}

func TestReconciler_ClusterwideWithNamespaceSelector(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	tcpProto := "TCP"

	// CiliumClusterwideNetworkPolicy: allow ingress from frontend namespace to backend pods
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "ccnp-ns-selector",
				Name: "clusterwide-ns-selector",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								Description: strPtr("Allow ingress from frontend namespace to backend pods"),
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "backend"},
								},
								Ingress: []*pb.CiliumPolicyIngressRule{
									{
										FromEndpoints: &pb.LabelSelectorList{
											Items: []*pb.LabelSelector{
												{MatchLabels: map[string]string{
													"k8s:io.kubernetes.pod.namespace": "frontend",
												}},
											},
										},
										ToPorts: []*pb.CiliumPolicyPortRule{
											{
												Ports: []*pb.CiliumPolicyPort{
													{Port: "8080", Protocol: &tcpProto},
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
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "clusterwide-ns-selector")
		return err == nil && obj != nil
	}, 10*time.Second, 100*time.Millisecond, "clusterwide ns-selector policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "clusterwide-ns-selector")
	require.NoError(t, err)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())

	expected := loadExpectedPolicy(t, "clusterwide-ns-selector.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "clusterwide-ns-selector")
	})
}

func TestReconciler_MultipleMutationsConverge(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	// Start with empty snapshot
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	time.Sleep(500 * time.Millisecond)

	// Mutation 1: create policy A
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_CreateObject{
					CreateObject: &pb.ConfiguredKubernetesObjectData{
						Id:        "cnp-multi-a",
						Name:      "e2e-multi-a",
		
						KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
							CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
								Specs: []*pb.CiliumPolicyRule{
									{
										EndpointSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"app": "a"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	})

	// Mutation 2: create policy B
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_CreateObject{
					CreateObject: &pb.ConfiguredKubernetesObjectData{
						Id:        "cnp-multi-b",
						Name:      "e2e-multi-b",
		
						KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
							CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
								Specs: []*pb.CiliumPolicyRule{
									{
										EndpointSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"app": "b"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	})

	// Wait for both policies to appear
	require.Eventually(t, func() bool {
		a, err1 := testClient.GetResource(ctx, ccnpGVR, "", "e2e-multi-a")
		b, err2 := testClient.GetResource(ctx, ccnpGVR, "", "e2e-multi-b")
		return err1 == nil && a != nil && err2 == nil && b != nil
	}, 10*time.Second, 100*time.Millisecond, "both policies should be applied")

	// Mutation 3: update policy A with new endpoint selector
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_UpdateObject{
					UpdateObject: &pb.ConfiguredKubernetesObjectData{
						Id:        "cnp-multi-a",
						Name:      "e2e-multi-a",
		
						KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
							CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
								Specs: []*pb.CiliumPolicyRule{
									{
										EndpointSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"app": "a-updated"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	})

	// Mutation 4: delete policy B
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_DeleteObject{
					DeleteObject: &pb.DeleteConfiguredKubernetesObject{
						Id: "cnp-multi-b",
					},
				},
			},
		},
	})

	// Verify policy A was updated
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-multi-a")
		if err != nil || obj == nil {
			return false
		}
		spec, ok := obj.Object["spec"].(map[string]any)
		if !ok {
			return false
		}
		es, ok := spec["endpointSelector"].(map[string]any)
		if !ok {
			return false
		}
		ml, ok := es["matchLabels"].(map[string]any)
		if !ok {
			return false
		}
		return ml["app"] == "a-updated"
	}, 10*time.Second, 100*time.Millisecond, "policy A should be updated with new endpoint selector")

	// Verify policy B was deleted
	require.Eventually(t, func() bool {
		_, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-multi-b")
		return err != nil
	}, 20*time.Second, 100*time.Millisecond, "policy B should be deleted")

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-multi-a")
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-multi-b")
	})
}

func TestReconciler_UpdateChangesSpec(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()
	boolTrue := true

	// Snapshot: create policy with enableDefaultDeny.Ingress = true, endpoint selector app=web
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:        "cnp-update-spec",
				Name:      "e2e-update-spec",

				Labels:    map[string]string{"env": "staging"},
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "web"},
								},
								EnableDefaultDeny: &pb.CiliumPolicyDefaultDeny{
									Ingress: &boolTrue,
								},
							},
						},
					},
				},
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	// Wait for the policy to appear and verify initial spec deeply
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-update-spec")
		return err == nil && obj != nil
	}, 10*time.Second, 100*time.Millisecond, "initial policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-update-spec")
	require.NoError(t, err)
	assert.Equal(t, "staging", obj.GetLabels()["env"])

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)
	es, ok := spec["endpointSelector"].(map[string]any)
	require.True(t, ok)
	ml, ok := es["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "web", ml["app"])

	// Update mutation: change endpoint selector, labels, and switch from ingress deny to egress deny
	boolEgress := true
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_UpdateObject{
					UpdateObject: &pb.ConfiguredKubernetesObjectData{
						Id:        "cnp-update-spec",
						Name:      "e2e-update-spec",
		
						Labels:    map[string]string{"env": "production"},
						KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
							CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
								Specs: []*pb.CiliumPolicyRule{
									{
										EndpointSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"app": "api-v2"},
										},
										EnableDefaultDeny: &pb.CiliumPolicyDefaultDeny{
											Egress: &boolEgress,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	})

	// Verify the spec was updated in K8s
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-update-spec")
		if err != nil || obj == nil {
			return false
		}
		spec, ok := obj.Object["spec"].(map[string]any)
		if !ok {
			return false
		}
		es, ok := spec["endpointSelector"].(map[string]any)
		if !ok {
			return false
		}
		ml, ok := es["matchLabels"].(map[string]any)
		if !ok {
			return false
		}
		return ml["app"] == "api-v2"
	}, 10*time.Second, 100*time.Millisecond, "policy spec should be updated after mutation")

	obj, err = testClient.GetResource(ctx, ccnpGVR, "", "e2e-update-spec")
	require.NoError(t, err)
	assert.Equal(t, "production", obj.GetLabels()["env"])
	assert.Equal(t, controller.ManagedByValue, obj.GetLabels()[controller.ManagedByLabel])
	assert.Equal(t, "cnp-update-spec", obj.GetLabels()[controller.CloudSecureIDLabel])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-update-spec")
	})
}

func TestReconciler_DeleteOnlyRemovesTargetPolicy(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	// Snapshot: two policies
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:        "cnp-del-keep",
				Name:      "e2e-del-keep",

				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "keep"},
								},
							},
						},
					},
				},
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:        "cnp-del-remove",
				Name:      "e2e-del-remove",

				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "remove"},
								},
							},
						},
					},
				},
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	// Wait for both to appear
	require.Eventually(t, func() bool {
		a, err1 := testClient.GetResource(ctx, ccnpGVR, "", "e2e-del-keep")
		b, err2 := testClient.GetResource(ctx, ccnpGVR, "", "e2e-del-remove")
		return err1 == nil && a != nil && err2 == nil && b != nil
	}, 10*time.Second, 100*time.Millisecond, "both policies should exist after snapshot")

	// Delete only the "remove" policy
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_DeleteObject{
					DeleteObject: &pb.DeleteConfiguredKubernetesObject{
						Id: "cnp-del-remove",
					},
				},
			},
		},
	})

	// Verify "remove" is deleted from K8s
	require.Eventually(t, func() bool {
		_, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-del-remove")
		return err != nil
	}, 20*time.Second, 100*time.Millisecond, "deleted policy should be removed from K8s")

	// Verify "keep" still exists and is untouched
	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-del-keep")
	require.NoError(t, err)
	assert.Equal(t, "e2e-del-keep", obj.GetName())
	assert.Equal(t, controller.ManagedByValue, obj.GetLabels()[controller.ManagedByLabel])
	assert.Equal(t, "cnp-del-keep", obj.GetLabels()[controller.CloudSecureIDLabel])

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)
	es, ok := spec["endpointSelector"].(map[string]any)
	require.True(t, ok)
	ml, ok := es["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "keep", ml["app"], "surviving policy spec should be unchanged")

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-del-keep")
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-del-remove")
	})
}

func TestReconciler_CreateVerifiesSpecContent(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	// Create a policy with rich spec: ingress rules, egress rules, labels, description
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:     "ccnp-spec-verify",
				Name:   "e2e-spec-verify",
				Labels: map[string]string{"team": "platform", "tier": "infra"},
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: func() *pb.KubernetesCiliumClusterwideNetworkPolicyData {
						boolTrue := true
						tcpProto := "TCP"
						return &pb.KubernetesCiliumClusterwideNetworkPolicyData{
							Specs: []*pb.CiliumPolicyRule{
								{
									Description: strPtr("Allow HTTP ingress from frontend, deny all egress"),
									EndpointSelector: &pb.LabelSelector{
										MatchLabels: map[string]string{"app": "backend"},
									},
									EnableDefaultDeny: &pb.CiliumPolicyDefaultDeny{
										Ingress: &boolTrue,
										Egress:  &boolTrue,
									},
									Ingress: []*pb.CiliumPolicyIngressRule{
										{
											FromEndpoints: &pb.LabelSelectorList{
												Items: []*pb.LabelSelector{
													{MatchLabels: map[string]string{"app": "frontend"}},
												},
											},
											ToPorts: []*pb.CiliumPolicyPortRule{
												{
													Ports: []*pb.CiliumPolicyPort{
														{Port: "80", Protocol: &tcpProto},
														{Port: "443", Protocol: &tcpProto},
													},
												},
											},
										},
									},
									Egress: []*pb.CiliumPolicyEgressRule{{}},
								},
							},
						}
					}(),
				},
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-spec-verify")
		return err == nil && obj != nil
	}, 10*time.Second, 100*time.Millisecond, "policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-spec-verify")
	require.NoError(t, err)

	// Verify metadata
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())
	assert.Equal(t, "platform", obj.GetLabels()["team"])
	assert.Equal(t, "infra", obj.GetLabels()["tier"])
	assert.Equal(t, controller.ManagedByValue, obj.GetLabels()[controller.ManagedByLabel])
	assert.Equal(t, "ccnp-spec-verify", obj.GetLabels()[controller.CloudSecureIDLabel])

	// Verify spec matches expected YAML
	expected := loadExpectedPolicy(t, "e2e-spec-verify.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-spec-verify")
	})
}

func TestReconciler_UpdatePreservesMetadata(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	// Create initial policy with labels
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:        "cnp-meta-preserve",
				Name:      "e2e-meta-preserve",

				Labels:    map[string]string{"version": "v1"},
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "svc"},
								},
							},
						},
					},
				},
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-meta-preserve")
		return err == nil && obj != nil
	}, 10*time.Second, 100*time.Millisecond, "initial policy should be applied")

	// Update via mutation: change spec and labels
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_UpdateObject{
					UpdateObject: &pb.ConfiguredKubernetesObjectData{
						Id:        "cnp-meta-preserve",
						Name:      "e2e-meta-preserve",
		
						Labels:    map[string]string{"version": "v2"},
						KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
							CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
								Specs: []*pb.CiliumPolicyRule{
									{
										EndpointSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"app": "svc-updated"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	})

	// Verify spec updated AND metadata labels are correct
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-meta-preserve")
		if err != nil || obj == nil {
			return false
		}
		spec, ok := obj.Object["spec"].(map[string]any)
		if !ok {
			return false
		}
		es, ok := spec["endpointSelector"].(map[string]any)
		if !ok {
			return false
		}
		ml, ok := es["matchLabels"].(map[string]any)
		if !ok {
			return false
		}
		return ml["app"] == "svc-updated"
	}, 10*time.Second, 100*time.Millisecond, "policy should be updated")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-meta-preserve")
	require.NoError(t, err)
	assert.Equal(t, "v2", obj.GetLabels()["version"])
	assert.Equal(t, controller.ManagedByValue, obj.GetLabels()[controller.ManagedByLabel])
	assert.Equal(t, "cnp-meta-preserve", obj.GetLabels()[controller.CloudSecureIDLabel])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-meta-preserve")
	})
}

func TestReconciler_CIDRGroupSnapshot(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	// Push config + CIDRGroup + snapshot complete
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "cidr-e2e-1",
				Name: "e2e-cidr-group",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumCidrGroup{
					CiliumCidrGroup: &pb.KubernetesCiliumCIDRGroupData{
						Spec: &pb.CiliumCIDRGroup{
							ExternalCidrs: []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
						},
					},
				},
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	// Wait for the CIDRGroup to appear in envtest
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, cidrGroupGVR, "", "e2e-cidr-group")
		return err == nil && obj != nil
	}, 10*time.Second, 100*time.Millisecond, "CIDRGroup should be applied to K8s")

	obj, err := testClient.GetResource(ctx, cidrGroupGVR, "", "e2e-cidr-group")
	require.NoError(t, err)
	assert.Equal(t, "CiliumCIDRGroup", obj.GetKind())
	assert.Equal(t, "e2e-cidr-group", obj.GetName())
	assert.Equal(t, controller.ManagedByValue, obj.GetLabels()[controller.ManagedByLabel])
	assert.Equal(t, "cidr-e2e-1", obj.GetLabels()[controller.CloudSecureIDLabel])

	expected := loadExpectedPolicy(t, "e2e-cidr-group.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, cidrGroupGVR, "", "e2e-cidr-group")
	})
}

func TestReconciler_CIDRGroupMutationCreateUpdateDelete(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	// Start with empty snapshot
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	time.Sleep(500 * time.Millisecond)

	// Create a CIDRGroup via mutation
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_CreateObject{
					CreateObject: &pb.ConfiguredKubernetesObjectData{
						Id:     "cidr-mut-1",
						Name:   "e2e-cidr-mut",
						Labels: map[string]string{"env": "test"},
						KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumCidrGroup{
							CiliumCidrGroup: &pb.KubernetesCiliumCIDRGroupData{
								Spec: &pb.CiliumCIDRGroup{
									ExternalCidrs: []string{"10.0.0.0/8"},
								},
							},
						},
					},
				},
			},
		},
	})

	// Wait for CIDRGroup to appear
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, cidrGroupGVR, "", "e2e-cidr-mut")
		return err == nil && obj != nil
	}, 10*time.Second, 100*time.Millisecond, "CIDRGroup should be created via mutation")

	obj, err := testClient.GetResource(ctx, cidrGroupGVR, "", "e2e-cidr-mut")
	require.NoError(t, err)
	assert.Equal(t, "CiliumCIDRGroup", obj.GetKind())
	assert.Equal(t, "test", obj.GetLabels()["env"])

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)
	cidrs, ok := spec["externalCIDRs"].([]any)
	require.True(t, ok)
	require.Len(t, cidrs, 1)
	assert.Equal(t, "10.0.0.0/8", cidrs[0])

	// Update: add more CIDRs
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_UpdateObject{
					UpdateObject: &pb.ConfiguredKubernetesObjectData{
						Id:     "cidr-mut-1",
						Name:   "e2e-cidr-mut",
						Labels: map[string]string{"env": "production"},
						KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumCidrGroup{
							CiliumCidrGroup: &pb.KubernetesCiliumCIDRGroupData{
								Spec: &pb.CiliumCIDRGroup{
									ExternalCidrs: []string{"10.0.0.0/8", "172.16.0.0/12"},
								},
							},
						},
					},
				},
			},
		},
	})

	// Verify the CIDRGroup was updated
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, cidrGroupGVR, "", "e2e-cidr-mut")
		if err != nil || obj == nil {
			return false
		}
		spec, ok := obj.Object["spec"].(map[string]any)
		if !ok {
			return false
		}
		cidrs, ok := spec["externalCIDRs"].([]any)
		if !ok {
			return false
		}
		return len(cidrs) == 2
	}, 10*time.Second, 100*time.Millisecond, "CIDRGroup should be updated with additional CIDRs")

	obj, err = testClient.GetResource(ctx, cidrGroupGVR, "", "e2e-cidr-mut")
	require.NoError(t, err)
	assert.Equal(t, "production", obj.GetLabels()["env"])

	// Delete the CIDRGroup
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_DeleteObject{
					DeleteObject: &pb.DeleteConfiguredKubernetesObject{
						Id: "cidr-mut-1",
					},
				},
			},
		},
	})

	// Verify the CIDRGroup was deleted
	require.Eventually(t, func() bool {
		_, err := testClient.GetResource(ctx, cidrGroupGVR, "", "e2e-cidr-mut")
		return err != nil
	}, 20*time.Second, 100*time.Millisecond, "CIDRGroup should be deleted from K8s")

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, cidrGroupGVR, "", "e2e-cidr-mut")
	})
}

// TestReconciler_ICMPPolicy tests that ICMP type fields are serialized correctly.
// Based on the Cilium host firewall example from:
// https://docs.cilium.io/en/latest/security/host-firewall/
// Cilium expects {"type": 8} or {"type": "EchoRequest"}, not {"typeInt": 8} or {"typeString": "EchoRequest"}.
func TestReconciler_ICMPPolicy(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	// CiliumClusterwideNetworkPolicy with ICMP rules, modeled after:
	//   apiVersion: "cilium.io/v2"
	//   kind: CiliumClusterwideNetworkPolicy
	//   metadata:
	//     name: "demo-host-policy"
	//   spec:
	//     nodeSelector:
	//       matchLabels:
	//         node-access: ssh
	//     ingress:
	//       - toPorts:
	//           - ports:
	//               - port: "22"
	//                 protocol: TCP
	//         icmps:
	//           - fields:
	//               - type: 8
	//                 family: IPv4
	//               - type: EchoRequest
	//                 family: IPv4
	tcpProto := "TCP"
	ipv4Family := "IPv4"

	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "cnp-icmp-1",
				Name: "demo-host-policy",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								NodeSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"node-access": "ssh"},
								},
								Ingress: []*pb.CiliumPolicyIngressRule{
									{
										FromEntities: []string{"cluster"},
									},
									{
										ToPorts: []*pb.CiliumPolicyPortRule{
											{
												Ports: []*pb.CiliumPolicyPort{
													{Port: "22", Protocol: &tcpProto},
												},
											},
										},
									},
									{
										Icmps: []*pb.CiliumPolicyICMPRule{
											{
												Fields: []*pb.CiliumPolicyICMPField{
													{Family: &ipv4Family, Type: &pb.CiliumPolicyICMPField_TypeInt{TypeInt: 8}},
													{Family: &ipv4Family, Type: &pb.CiliumPolicyICMPField_TypeString{TypeString: "EchoRequest"}},
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
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "demo-host-policy")
		return err == nil && obj != nil
	}, 10*time.Second, 100*time.Millisecond, "ICMP policy should be applied to K8s")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "demo-host-policy")
	require.NoError(t, err)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())

	expected := loadExpectedPolicy(t, "demo-host-policy.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "demo-host-policy")
	})
}

// TestReconciler_SingleSpecSerializesAsSpec verifies that a policy with one rule
// serializes as "spec" (singular), matching what users write by hand.
// Based on: https://docs.cilium.io/en/latest/security/policy/language/#dns-based
func TestReconciler_SingleSpecSerializesAsSpec(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()
	udpProto := "UDP"

	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "cnp-single-spec",
				Name: "global-default-deny-and-core-egress",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								Description: strPtr("Enforce zero-trust egress cluster-wide while maintaining core DNS and API access."),
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{},
								},
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
									{
										ToEntities: []string{"kube-apiserver"},
									},
								},
							},
						},
					},
				},
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "global-default-deny-and-core-egress")
		return err == nil && obj != nil
	}, 10*time.Second, 100*time.Millisecond, "single-spec policy should be applied to K8s")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "global-default-deny-and-core-egress")
	require.NoError(t, err)

	// Single rule should serialize as "spec", not "specs"
	_, hasSpec := obj.Object["spec"]
	_, hasSpecs := obj.Object["specs"]
	assert.True(t, hasSpec, "single rule should be stored under 'spec'")
	assert.False(t, hasSpecs, "single rule should not use 'specs'")

	expected := loadExpectedPolicy(t, "global-default-deny-and-core-egress.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "global-default-deny-and-core-egress")
	})
}

// TestReconciler_MultipleSpecsSerializesAsSpecs verifies that a policy with multiple
// rules serializes as "specs" (plural array), not "spec".
func TestReconciler_MultipleSpecsSerializesAsSpecs(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()
	tcpProto := "TCP"

	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "cnp-multi-specs",
				Name: "multi-rule-policy",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
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
											{
												Ports: []*pb.CiliumPolicyPort{
													{Port: "80", Protocol: &tcpProto},
												},
											},
										},
									},
								},
							},
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "api"},
								},
								Egress: []*pb.CiliumPolicyEgressRule{
									{
										ToEndpoints: &pb.LabelSelectorList{
											Items: []*pb.LabelSelector{
												{MatchLabels: map[string]string{"app": "database"}},
											},
										},
										ToPorts: []*pb.CiliumPolicyPortRule{
											{
												Ports: []*pb.CiliumPolicyPort{
													{Port: "5432", Protocol: &tcpProto},
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
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "multi-rule-policy")
		return err == nil && obj != nil
	}, 10*time.Second, 100*time.Millisecond, "multi-specs policy should be applied to K8s")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "multi-rule-policy")
	require.NoError(t, err)

	// Multiple rules should serialize as "specs", not "spec"
	_, hasSpec := obj.Object["spec"]
	_, hasSpecs := obj.Object["specs"]
	assert.False(t, hasSpec, "multiple rules should not use 'spec'")
	assert.True(t, hasSpecs, "multiple rules should be stored under 'specs'")

	expected := loadExpectedPolicy(t, "multi-rule-policy.yaml")
	assert.Equal(t, expected["specs"], obj.Object["specs"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "multi-rule-policy")
	})
}

// TestReconciler_CIDRSetWithExceptions verifies toCIDRSet with except ranges.
// Based on: https://docs.cilium.io/en/latest/security/policy/language/#cidr-based
//
//	egress:
//	  - toCIDRSet:
//	      - cidr: 10.0.0.0/8
//	        except:
//	          - 10.96.0.0/12
//	  - toCIDR:
//	      - 192.168.1.0/24
func TestReconciler_CIDRSetWithExceptions(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()
	cidr := "10.0.0.0/8"

	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "cnp-cidr-set",
				Name: "cidr-set-policy",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "myService"},
								},
								Egress: []*pb.CiliumPolicyEgressRule{
									{
										ToCidr: []string{"20.1.1.1/32"},
									},
									{
										ToCidrSet: []*pb.CiliumPolicyCIDRSet{
											{
												Cidr:   &cidr,
												Except: []string{"10.96.0.0/12"},
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
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "cidr-set-policy")
		return err == nil && obj != nil
	}, 10*time.Second, 100*time.Millisecond, "CIDR set policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "cidr-set-policy")
	require.NoError(t, err)

	expected := loadExpectedPolicy(t, "cidr-set-policy.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "cidr-set-policy")
	})
}

// TestReconciler_FQDNEgressWithDNSProxy verifies toFQDNs and DNS L7 rules.
// Based on: https://docs.cilium.io/en/latest/security/policy/language/#dns-based
//
//	egress:
//	  - toEndpoints:
//	      - matchLabels:
//	          "k8s:io.kubernetes.pod.namespace": kube-system
//	          k8s-app: kube-dns
//	    toPorts:
//	      - ports:
//	          - port: "53"
//	            protocol: UDP
//	        rules:
//	          dns:
//	            - matchPattern: "*"
//	  - toFQDNs:
//	      - matchName: "my-remote-service.com"
//	      - matchPattern: "*.api.example.com"
//	    toPorts:
//	      - ports:
//	          - port: "443"
//	            protocol: TCP
func TestReconciler_FQDNEgressWithDNSProxy(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()
	udpProto := "UDP"
	tcpProto := "TCP"
	matchName := "my-remote-service.com"
	matchPattern := "*.api.example.com"

	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "cnp-fqdn-dns",
				Name: "fqdn-dns-policy",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "external-client"},
								},
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
									{
										ToFqdns: []*pb.CiliumPolicyFQDNSelector{
											{MatchName: &matchName},
											{MatchPattern: &matchPattern},
										},
										ToPorts: []*pb.CiliumPolicyPortRule{
											{
												Ports: []*pb.CiliumPolicyPort{
													{Port: "443", Protocol: &tcpProto},
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
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "fqdn-dns-policy")
		return err == nil && obj != nil
	}, 10*time.Second, 100*time.Millisecond, "FQDN policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "fqdn-dns-policy")
	require.NoError(t, err)

	expected := loadExpectedPolicy(t, "fqdn-dns-policy.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "fqdn-dns-policy")
	})
}

// TestReconciler_HostFirewallWithEntitiesAndDeny verifies a host firewall policy
// combining fromEntities, ingressDeny, and nodeSelector.
// Based on: https://docs.cilium.io/en/latest/security/host-firewall/
//
//	spec:
//	  nodeSelector:
//	    matchLabels:
//	      node-access: locked
//	  ingress:
//	    - fromEntities:
//	        - cluster
//	    - fromCIDR:
//	        - 10.0.0.0/8
//	      toPorts:
//	        - ports:
//	            - port: "22"
//	              protocol: TCP
//	  ingressDeny:
//	    - fromCIDR:
//	        - 192.168.0.0/16
//	      toPorts:
//	        - ports:
//	            - port: "23"
//	              protocol: TCP
func TestReconciler_HostFirewallWithEntitiesAndDeny(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()
	tcpProto := "TCP"

	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "cnp-host-fw",
				Name: "host-firewall-locked",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								NodeSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"node-access": "locked"},
								},
								Ingress: []*pb.CiliumPolicyIngressRule{
									{
										FromEntities: []string{"cluster"},
									},
									{
										FromCidr: []string{"10.0.0.0/8"},
										ToPorts: []*pb.CiliumPolicyPortRule{
											{
												Ports: []*pb.CiliumPolicyPort{
													{Port: "22", Protocol: &tcpProto},
												},
											},
										},
									},
								},
								IngressDeny: []*pb.CiliumPolicyIngressRule{
									{
										FromCidr: []string{"192.168.0.0/16"},
										ToPorts: []*pb.CiliumPolicyPortRule{
											{
												Ports: []*pb.CiliumPolicyPort{
													{Port: "23", Protocol: &tcpProto},
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
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "host-firewall-locked")
		return err == nil && obj != nil
	}, 10*time.Second, 100*time.Millisecond, "host firewall policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "host-firewall-locked")
	require.NoError(t, err)

	expected := loadExpectedPolicy(t, "host-firewall-locked.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "host-firewall-locked")
	})
}
