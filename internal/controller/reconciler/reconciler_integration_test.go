// Copyright 2026 Illumio, Inc. All Rights Reserved.

//go:build envtest

package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller"
)

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

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)

	ingress, ok := spec["ingress"].([]any)
	require.True(t, ok, "spec should have ingress rules")
	require.Len(t, ingress, 1)

	ingressRule, ok := ingress[0].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, ingressRule, "fromEndpoints")
	assert.Contains(t, ingressRule, "toPorts")

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
				Id:        "cnp-deny-egress",
				Name:      "deny-all-egress",

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

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, spec, "endpointSelector")
	assert.Contains(t, spec, "egress")

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

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)

	ingress, ok := spec["ingress"].([]any)
	require.True(t, ok, "spec should have ingress rules")
	require.Len(t, ingress, 1)

	ingressRule, ok := ingress[0].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, ingressRule, "fromEndpoints")
	assert.Contains(t, ingressRule, "toPorts")

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
	boolTrue := true
	tcpProto := "TCP"

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
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
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

	// Verify spec structure
	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)

	assert.Equal(t, "Allow HTTP ingress from frontend, deny all egress", spec["description"])

	// Verify endpointSelector
	es, ok := spec["endpointSelector"].(map[string]any)
	require.True(t, ok)
	ml, ok := es["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "backend", ml["app"])

	// Verify ingress rules
	ingress, ok := spec["ingress"].([]any)
	require.True(t, ok)
	require.Len(t, ingress, 1)
	ingressRule, ok := ingress[0].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, ingressRule, "fromEndpoints")
	assert.Contains(t, ingressRule, "toPorts")

	// Verify toPorts has correct port values
	toPorts, ok := ingressRule["toPorts"].([]any)
	require.True(t, ok)
	require.Len(t, toPorts, 1)
	portRule, ok := toPorts[0].(map[string]any)
	require.True(t, ok)
	ports, ok := portRule["ports"].([]any)
	require.True(t, ok)
	require.Len(t, ports, 2)

	port0, ok := ports[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "80", port0["port"])
	assert.Equal(t, "TCP", port0["protocol"])

	port1, ok := ports[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "443", port1["port"])
	assert.Equal(t, "TCP", port1["protocol"])

	// Verify egress rules
	egress, ok := spec["egress"].([]any)
	require.True(t, ok)
	require.Len(t, egress, 1)

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
