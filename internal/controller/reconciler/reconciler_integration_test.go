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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kjson "k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/yaml"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/convert"
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

	return parsed
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
				Id:   "cnp-e2e-1",
				Name: "e2e-snapshot-policy",

				Labels: map[string]string{"env": "e2e"},
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
								Ingress: []*pb.CiliumPolicyIngressRule{{}},
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
	}, 20*time.Second, 100*time.Millisecond, "policy should be applied to K8s")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-snapshot-policy")
	require.NoError(t, err)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())
	assert.Equal(t, "e2e-snapshot-policy", obj.GetName())
	assert.Equal(t, "e2e", obj.GetLabels()["env"])
	assert.Equal(t, convert.ManagedByValue, obj.GetLabels()[convert.ManagedByLabel])
	assert.Equal(t, "cnp-e2e-1", obj.GetLabels()[convert.CloudSecureIDLabel])

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
						Id:   "cnp-e2e-2",
						Name: "e2e-mutation-policy",

						KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
							CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
								Specs: []*pb.CiliumPolicyRule{
									{
										EndpointSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"app": "api"},
										},
										Ingress: []*pb.CiliumPolicyIngressRule{{}},
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
	}, 20*time.Second, 100*time.Millisecond, "mutation policy should be applied to K8s")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-mutation-policy")
	require.NoError(t, err)
	assert.Equal(t, "e2e-mutation-policy", obj.GetName())
	assert.Equal(t, "cnp-e2e-2", obj.GetLabels()[convert.CloudSecureIDLabel])

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
				Id:   "cnp-e2e-3",
				Name: "e2e-delete-policy",

				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "temp"},
								},
								Ingress: []*pb.CiliumPolicyIngressRule{{}},
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
	}, 20*time.Second, 100*time.Millisecond, "policy should be applied before delete")

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
		return apierrors.IsNotFound(err)
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
				Id:   "cnp-ssa",
				Name: "e2e-ssa-policy",

				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "ssa"},
								},
								Ingress: []*pb.CiliumPolicyIngressRule{{}},
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
	}, 20*time.Second, 100*time.Millisecond, "policy should be applied to K8s")

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
						Id:   "cnp-multi-a",
						Name: "e2e-multi-a",

						KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
							CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
								Specs: []*pb.CiliumPolicyRule{
									{
										EndpointSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"app": "a"},
										},
										Ingress: []*pb.CiliumPolicyIngressRule{{}},
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
						Id:   "cnp-multi-b",
						Name: "e2e-multi-b",

						KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
							CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
								Specs: []*pb.CiliumPolicyRule{
									{
										EndpointSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"app": "b"},
										},
										Ingress: []*pb.CiliumPolicyIngressRule{{}},
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
	}, 20*time.Second, 100*time.Millisecond, "both policies should be applied")

	// Mutation 3: update policy A with new endpoint selector
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_UpdateObject{
					UpdateObject: &pb.ConfiguredKubernetesObjectData{
						Id:   "cnp-multi-a",
						Name: "e2e-multi-a",

						KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
							CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
								Specs: []*pb.CiliumPolicyRule{
									{
										EndpointSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"app": "a-updated"},
										},
										Ingress: []*pb.CiliumPolicyIngressRule{{}},
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
	}, 20*time.Second, 100*time.Millisecond, "policy A should be updated with new endpoint selector")

	// Verify policy B was deleted
	require.Eventually(t, func() bool {
		_, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-multi-b")
		return apierrors.IsNotFound(err)
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
				Id:   "cnp-update-spec",
				Name: "e2e-update-spec",

				Labels: map[string]string{"env": "staging"},
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
								Ingress: []*pb.CiliumPolicyIngressRule{{}},
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
	}, 20*time.Second, 100*time.Millisecond, "initial policy should be applied")

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
						Id:   "cnp-update-spec",
						Name: "e2e-update-spec",

						Labels: map[string]string{"env": "production"},
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
										Egress: []*pb.CiliumPolicyEgressRule{{}},
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
	}, 20*time.Second, 100*time.Millisecond, "policy spec should be updated after mutation")

	obj, err = testClient.GetResource(ctx, ccnpGVR, "", "e2e-update-spec")
	require.NoError(t, err)
	assert.Equal(t, "production", obj.GetLabels()["env"])
	assert.Equal(t, convert.ManagedByValue, obj.GetLabels()[convert.ManagedByLabel])
	assert.Equal(t, "cnp-update-spec", obj.GetLabels()[convert.CloudSecureIDLabel])

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
				Id:   "cnp-del-keep",
				Name: "e2e-del-keep",

				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "keep"},
								},
								Ingress: []*pb.CiliumPolicyIngressRule{{}},
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
				Id:   "cnp-del-remove",
				Name: "e2e-del-remove",

				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "remove"},
								},
								Ingress: []*pb.CiliumPolicyIngressRule{{}},
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
	}, 20*time.Second, 100*time.Millisecond, "both policies should exist after snapshot")

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
		return apierrors.IsNotFound(err)
	}, 20*time.Second, 100*time.Millisecond, "deleted policy should be removed from K8s")

	// Verify "keep" still exists and is untouched
	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-del-keep")
	require.NoError(t, err)
	assert.Equal(t, "e2e-del-keep", obj.GetName())
	assert.Equal(t, convert.ManagedByValue, obj.GetLabels()[convert.ManagedByLabel])
	assert.Equal(t, "cnp-del-keep", obj.GetLabels()[convert.CloudSecureIDLabel])

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
				Id:   "cnp-meta-preserve",
				Name: "e2e-meta-preserve",

				Labels: map[string]string{"version": "v1"},
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "svc"},
								},
								Ingress: []*pb.CiliumPolicyIngressRule{{}},
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
	}, 20*time.Second, 100*time.Millisecond, "initial policy should be applied")

	// Update via mutation: change spec and labels
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_UpdateObject{
					UpdateObject: &pb.ConfiguredKubernetesObjectData{
						Id:   "cnp-meta-preserve",
						Name: "e2e-meta-preserve",

						Labels: map[string]string{"version": "v2"},
						KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
							CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
								Specs: []*pb.CiliumPolicyRule{
									{
										EndpointSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"app": "svc-updated"},
										},
										Ingress: []*pb.CiliumPolicyIngressRule{{}},
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
	}, 20*time.Second, 100*time.Millisecond, "policy should be updated")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-meta-preserve")
	require.NoError(t, err)
	assert.Equal(t, "v2", obj.GetLabels()["version"])
	assert.Equal(t, convert.ManagedByValue, obj.GetLabels()[convert.ManagedByLabel])
	assert.Equal(t, "cnp-meta-preserve", obj.GetLabels()[convert.CloudSecureIDLabel])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-meta-preserve")
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
	}, 20*time.Second, 100*time.Millisecond, "CIDRGroup should be created via mutation")

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
	}, 20*time.Second, 100*time.Millisecond, "CIDRGroup should be updated with additional CIDRs")

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
		return apierrors.IsNotFound(err)
	}, 20*time.Second, 100*time.Millisecond, "CIDRGroup should be deleted from K8s")

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, cidrGroupGVR, "", "e2e-cidr-mut")
	})
}

// Tests below use policies from the official Cilium examples repository:
// https://github.com/cilium/cilium/tree/main/examples/policies/kubernetes

// TestReconciler_CiliumExample_ClusterscopePolicy tests the clusterwide policy example.
// Source: https://github.com/cilium/cilium/blob/main/examples/policies/kubernetes/clusterwide/clusterscope-policy.yaml
func TestReconciler_CiliumExample_ClusterscopePolicy(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

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
				Id:   "ccnp-clusterscope",
				Name: "clusterwide-policy-example",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								Description: strPtr("Policy for selective ingress allow to a pod from only a pod with given label"),
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
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "clusterwide-policy-example")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "clusterscope policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "clusterwide-policy-example")
	require.NoError(t, err)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())
	assert.Equal(t, convert.ManagedByValue, obj.GetLabels()[convert.ManagedByLabel])
	assert.Equal(t, "ccnp-clusterscope", obj.GetLabels()[convert.CloudSecureIDLabel])

	expected := loadExpectedPolicy(t, "clusterscope-policy.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "clusterwide-policy-example")
	})
}

// TestReconciler_CiliumExample_HealthChecks tests the Cilium health checks policy.
// Source: https://github.com/cilium/cilium/blob/main/examples/policies/kubernetes/clusterwide/health.yaml
func TestReconciler_CiliumExample_HealthChecks(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

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
				Id:   "ccnp-health",
				Name: "cilium-health-checks",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
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
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "cilium-health-checks")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "health checks policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "cilium-health-checks")
	require.NoError(t, err)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())
	assert.Equal(t, convert.ManagedByValue, obj.GetLabels()[convert.ManagedByLabel])
	assert.Equal(t, "ccnp-health", obj.GetLabels()[convert.CloudSecureIDLabel])

	expected := loadExpectedPolicy(t, "cilium-health-checks.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "cilium-health-checks")
	})
}

// TestReconciler_CiliumExample_CrossNamespace tests the cross-namespace policy example.
// Source: https://github.com/cilium/cilium/blob/main/examples/policies/kubernetes/namespace/namespace-policy.yaml
// Adapted to CiliumClusterwideNetworkPolicy for envtest (original is a namespaced CiliumNetworkPolicy).
func TestReconciler_CiliumExample_CrossNamespace(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

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
				Id:   "ccnp-cross-ns",
				Name: "cross-namespace-policy",
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
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "cross-namespace-policy")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "cross-namespace policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "cross-namespace-policy")
	require.NoError(t, err)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())
	assert.Equal(t, convert.ManagedByValue, obj.GetLabels()[convert.ManagedByLabel])
	assert.Equal(t, "ccnp-cross-ns", obj.GetLabels()[convert.CloudSecureIDLabel])

	expected := loadExpectedPolicy(t, "cross-namespace-policy.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "cross-namespace-policy")
	})
}

// TestReconciler_CiliumExample_AllowToKubeDNS tests the kube-dns egress policy example.
// Source: https://github.com/cilium/cilium/blob/main/examples/policies/kubernetes/namespace/kubedns-policy.yaml
// Adapted to CiliumClusterwideNetworkPolicy for envtest (original is a namespaced CiliumNetworkPolicy).
func TestReconciler_CiliumExample_AllowToKubeDNS(t *testing.T) {
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
				Id:   "ccnp-kubedns",
				Name: "allow-to-kubedns",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
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
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "allow-to-kubedns")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "kubedns policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "allow-to-kubedns")
	require.NoError(t, err)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())
	assert.Equal(t, convert.ManagedByValue, obj.GetLabels()[convert.ManagedByLabel])
	assert.Equal(t, "ccnp-kubedns", obj.GetLabels()[convert.CloudSecureIDLabel])

	expected := loadExpectedPolicy(t, "allow-to-kubedns.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "allow-to-kubedns")
	})
}

// TestReconciler_CiliumExample_WildcardFromEndpoints tests the clusterwide wildcard ingress policy.
// Source: https://github.com/cilium/cilium/blob/main/examples/policies/kubernetes/clusterwide/wildcard-from-endpoints.yaml
func TestReconciler_CiliumExample_WildcardFromEndpoints(t *testing.T) {
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
				Id:   "ccnp-wildcard",
				Name: "wildcard-from-endpoints",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								Description: strPtr("Policy for ingress allow to kube-dns from all Cilium managed endpoints in the cluster"),
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
	}, 20*time.Second, 100*time.Millisecond, "wildcard-from-endpoints policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "wildcard-from-endpoints")
	require.NoError(t, err)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())
	assert.Equal(t, convert.ManagedByValue, obj.GetLabels()[convert.ManagedByLabel])

	expected := loadExpectedPolicy(t, "wildcard-from-endpoints.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "wildcard-from-endpoints")
	})
}

// TestReconciler_CiliumExample_ExternalLockdown tests ingressDeny from world + ingress from all.
// Source: https://github.com/cilium/cilium/blob/main/examples/policies/l3/entities/from_world_deny.yaml
func TestReconciler_CiliumExample_ExternalLockdown(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

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
				Id:   "ccnp-ext-lockdown",
				Name: "external-lockdown",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{},
								},
								IngressDeny: []*pb.CiliumPolicyIngressRule{
									{
										FromEntities: []string{"world"},
									},
								},
								Ingress: []*pb.CiliumPolicyIngressRule{
									{
										FromEntities: []string{"all"},
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
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "external-lockdown")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "external-lockdown policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "external-lockdown")
	require.NoError(t, err)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())
	assert.Equal(t, convert.ManagedByValue, obj.GetLabels()[convert.ManagedByLabel])

	expected := loadExpectedPolicy(t, "external-lockdown.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "external-lockdown")
	})
}

// TestReconciler_CiliumExample_Init tests a CCNP with specs (plural) and reserved:init selector.
// Source: https://github.com/cilium/cilium/blob/main/examples/policies/l4/init.yaml
func TestReconciler_CiliumExample_Init(t *testing.T) {
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
				Id:   "ccnp-init",
				Name: "init",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"reserved:init": ""},
								},
								Ingress: []*pb.CiliumPolicyIngressRule{
									{
										FromEntities: []string{"host"},
									},
								},
								Egress: []*pb.CiliumPolicyEgressRule{
									{
										ToEntities: []string{"all"},
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
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "init")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "init policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "init")
	require.NoError(t, err)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())
	assert.Equal(t, convert.ManagedByValue, obj.GetLabels()[convert.ManagedByLabel])

	expected := loadExpectedPolicy(t, "init.yaml")
	// Fixture uses "specs" (plural array) matching the original Cilium YAML,
	// but the reconciler normalizes a single-element Specs list to "spec" (singular).
	expectedSpecs := expected["specs"].([]any)
	assert.Equal(t, expectedSpecs[0], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "init")
	})
}

// TestReconciler_CiliumExample_DemoHostPolicy tests a host firewall policy with nodeSelector, entities, and ICMP.
// Source: https://github.com/cilium/cilium/blob/main/examples/policies/host/demo-host-policy.yaml
func TestReconciler_CiliumExample_DemoHostPolicy(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()
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
				Id:   "ccnp-demo-host",
				Name: "demo-host-policy",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								Description: strPtr(""),
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
	}, 20*time.Second, 100*time.Millisecond, "demo-host-policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "demo-host-policy")
	require.NoError(t, err)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())
	assert.Equal(t, convert.ManagedByValue, obj.GetLabels()[convert.ManagedByLabel])

	expected := loadExpectedPolicy(t, "demo-host-policy.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "demo-host-policy")
	})
}

// TestReconciler_CiliumExample_NamespaceLabelsPolicy tests namespace label-based selection.
// Source: https://github.com/cilium/cilium/blob/main/examples/policies/kubernetes/namespace/namespace-labels-policy.yaml
func TestReconciler_CiliumExample_NamespaceLabelsPolicy(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()
	ns := "default"

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
				Id:        "cnp-ns-labels",
				Name:      "alliance-only",
				Namespace: &ns,
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
					CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"name": "rebel-base"},
								},
								Ingress: []*pb.CiliumPolicyIngressRule{
									{
										FromEndpoints: &pb.LabelSelectorList{
											Items: []*pb.LabelSelector{
												{MatchLabels: map[string]string{
													"io.cilium.k8s.namespace.labels.faction": "alliance",
												}},
											},
										},
									},
								},
								Egress: []*pb.CiliumPolicyEgressRule{
									{
										ToEndpoints: &pb.LabelSelectorList{
											Items: []*pb.LabelSelector{
												{MatchLabels: map[string]string{
													"io.cilium.k8s.namespace.labels.faction": "alliance",
												}},
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
		obj, err := testClient.GetResource(ctx, cnpGVR, ns, "alliance-only")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "namespace-labels policy should be applied")

	obj, err := testClient.GetResource(ctx, cnpGVR, ns, "alliance-only")
	require.NoError(t, err)
	assert.Equal(t, "CiliumNetworkPolicy", obj.GetKind())
	assert.Equal(t, convert.ManagedByValue, obj.GetLabels()[convert.ManagedByLabel])
	assert.Equal(t, "cnp-ns-labels", obj.GetLabels()[convert.CloudSecureIDLabel])

	expected := loadExpectedPolicy(t, "namespace-labels-policy.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, cnpGVR, ns, "alliance-only")
	})
}

// TestReconciler_CiliumExample_ICMPRule tests ICMP type fields with both integer and string types.
// Source: https://github.com/cilium/cilium/blob/main/examples/policies/l4/icmp.yaml
func TestReconciler_CiliumExample_ICMPRule(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()
	ns := "default"
	ipv4Family := "IPv4"
	ipv6Family := "IPv6"

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
				Id:        "cnp-icmp",
				Name:      "icmp-rule",
				Namespace: &ns,
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
					CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "myService"},
								},
								Egress: []*pb.CiliumPolicyEgressRule{
									{
										Icmps: []*pb.CiliumPolicyICMPRule{
											{
												Fields: []*pb.CiliumPolicyICMPField{
													{Family: &ipv4Family, Type: &pb.CiliumPolicyICMPField_TypeInt{TypeInt: 8}},
													{Family: &ipv6Family, Type: &pb.CiliumPolicyICMPField_TypeString{TypeString: "EchoRequest"}},
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
		obj, err := testClient.GetResource(ctx, cnpGVR, ns, "icmp-rule")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "ICMP rule policy should be applied")

	obj, err := testClient.GetResource(ctx, cnpGVR, ns, "icmp-rule")
	require.NoError(t, err)
	assert.Equal(t, "CiliumNetworkPolicy", obj.GetKind())
	assert.Equal(t, convert.ManagedByValue, obj.GetLabels()[convert.ManagedByLabel])

	expected := loadExpectedPolicy(t, "icmp-rule.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, cnpGVR, ns, "icmp-rule")
	})
}

// TestReconciler_CiliumExample_CIDRRule tests toCIDR and toCIDRSet with except ranges.
// Source: https://github.com/cilium/cilium/blob/main/examples/policies/l3/cidr/cidr.yaml
func TestReconciler_CiliumExample_CIDRRule(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()
	ns := "default"
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
				Id:        "cnp-cidr",
				Name:      "cidr-rule",
				Namespace: &ns,
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
					CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
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
		obj, err := testClient.GetResource(ctx, cnpGVR, ns, "cidr-rule")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "CIDR rule policy should be applied")

	obj, err := testClient.GetResource(ctx, cnpGVR, ns, "cidr-rule")
	require.NoError(t, err)
	assert.Equal(t, "CiliumNetworkPolicy", obj.GetKind())
	assert.Equal(t, convert.ManagedByValue, obj.GetLabels()[convert.ManagedByLabel])

	expected := loadExpectedPolicy(t, "cidr-rule.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, cnpGVR, ns, "cidr-rule")
	})
}

// TestReconciler_CiliumExample_MatchExpressionsAND tests matchExpressions with AND semantics on namespace labels.
// Source: https://github.com/cilium/cilium/blob/main/examples/policies/l3/match-expressions/and-statement.yaml
func TestReconciler_CiliumExample_MatchExpressionsAND(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()
	ns := "default"

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
				Id:        "cnp-match-and",
				Name:      "and-statement-policy",
				Namespace: &ns,
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
					CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{},
								},
								Ingress: []*pb.CiliumPolicyIngressRule{
									{
										FromEndpoints: &pb.LabelSelectorList{
											Items: []*pb.LabelSelector{
												{
													MatchExpressions: []*pb.LabelSelectorRequirement{
														{
															Key:      "k8s:io.kubernetes.pod.namespace",
															Operator: "In",
															Values:   []string{"production"},
														},
														{
															Key:      "k8s:cilium.example.com/policy",
															Operator: "In",
															Values:   []string{"strict"},
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
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, cnpGVR, ns, "and-statement-policy")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "matchExpressions AND policy should be applied")

	obj, err := testClient.GetResource(ctx, cnpGVR, ns, "and-statement-policy")
	require.NoError(t, err)
	assert.Equal(t, "CiliumNetworkPolicy", obj.GetKind())
	assert.Equal(t, convert.ManagedByValue, obj.GetLabels()[convert.ManagedByLabel])

	expected := loadExpectedPolicy(t, "and-statement.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, cnpGVR, ns, "and-statement-policy")
	})
}

// TestReconciler_ExternalAnnotationsSurviveReApply verifies that annotations added by an
// external actor (e.g. kubectl, another controller) are preserved when the reconciler
// re-applies the object via SSA. The reconciler's field manager only owns the fields it
// sends; external annotations belong to a different field manager and must not be removed.
func TestReconciler_ExternalAnnotationsSurviveReApply(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	// Step 1: Apply initial policy with operator-owned annotation
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
				Id:          "cnp-ext-ann",
				Name:        "e2e-ext-annotations",
				Annotations: map[string]string{"operator-note": "managed"},
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "annotated"},
								},
								Ingress: []*pb.CiliumPolicyIngressRule{{}},
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
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-ext-annotations")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "policy should be applied")

	// Step 2: External actor adds an annotation via merge patch
	patch := []byte(`{"metadata":{"annotations":{"external-controller":"v1"}}}`)
	_, err := testClient.GetDynamicClient().Resource(ccnpGVR).Patch(
		ctx, "e2e-ext-annotations", types.MergePatchType, patch, metav1.PatchOptions{},
	)
	require.NoError(t, err)

	// Verify external annotation was added
	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-ext-annotations")
	require.NoError(t, err)
	assert.Equal(t, "v1", obj.GetAnnotations()["external-controller"])
	assert.Equal(t, "managed", obj.GetAnnotations()["operator-note"])

	// Step 3: Reconciler re-applies via update mutation (spec change triggers SSA apply)
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_UpdateObject{
					UpdateObject: &pb.ConfiguredKubernetesObjectData{
						Id:          "cnp-ext-ann",
						Name:        "e2e-ext-annotations",
						Annotations: map[string]string{"operator-note": "managed-v2"},
						KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
							CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
								Specs: []*pb.CiliumPolicyRule{
									{
										EndpointSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"app": "annotated-v2"},
										},
										Ingress: []*pb.CiliumPolicyIngressRule{{}},
									},
								},
							},
						},
					},
				},
			},
		},
	})

	// Wait for the spec update to land
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-ext-annotations")
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
		return ml["app"] == "annotated-v2"
	}, 20*time.Second, 100*time.Millisecond, "policy should be updated after mutation")

	// Verify: external annotation survives, operator annotation updated
	obj, err = testClient.GetResource(ctx, ccnpGVR, "", "e2e-ext-annotations")
	require.NoError(t, err)
	assert.Equal(t, "v1", obj.GetAnnotations()["external-controller"],
		"external annotation should survive SSA re-apply")
	assert.Equal(t, "managed-v2", obj.GetAnnotations()["operator-note"],
		"operator annotation should be updated")

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-ext-annotations")
	})
}

// TestReconciler_ExternalAnnotationDeletedByOperator verifies that when the operator
// removes an annotation it previously owned (by omitting it from the apply), SSA
// releases ownership of that key without affecting external annotations.
func TestReconciler_ExternalAnnotationDeletedByOperator(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	// Step 1: Apply policy with two operator annotations
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
				Id:          "cnp-ann-del",
				Name:        "e2e-ann-delete",
				Annotations: map[string]string{"keep": "yes", "remove-me": "temporary"},
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "ann-del"},
								},
								Ingress: []*pb.CiliumPolicyIngressRule{{}},
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
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-ann-delete")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-ann-delete")
	require.NoError(t, err)
	assert.Equal(t, "yes", obj.GetAnnotations()["keep"])
	assert.Equal(t, "temporary", obj.GetAnnotations()["remove-me"])

	// Step 2: External actor adds annotation
	patch := []byte(`{"metadata":{"annotations":{"external":"stays"}}}`)
	_, err = testClient.GetDynamicClient().Resource(ccnpGVR).Patch(
		ctx, "e2e-ann-delete", types.MergePatchType, patch, metav1.PatchOptions{},
	)
	require.NoError(t, err)

	// Step 3: Operator re-applies with "remove-me" annotation dropped and annotations nil
	// (omitting annotations entirely releases SSA ownership)
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_UpdateObject{
					UpdateObject: &pb.ConfiguredKubernetesObjectData{
						Id:   "cnp-ann-del",
						Name: "e2e-ann-delete",
						// Annotations set to nil — operator releases all annotation ownership
						KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
							CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
								Specs: []*pb.CiliumPolicyRule{
									{
										EndpointSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"app": "ann-del-v2"},
										},
										Ingress: []*pb.CiliumPolicyIngressRule{{}},
									},
								},
							},
						},
					},
				},
			},
		},
	})

	// Wait for spec update
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-ann-delete")
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
		return ml["app"] == "ann-del-v2"
	}, 20*time.Second, 100*time.Millisecond, "policy should be updated")

	// Verify: external annotation survives, operator annotations released
	obj, err = testClient.GetResource(ctx, ccnpGVR, "", "e2e-ann-delete")
	require.NoError(t, err)
	assert.Equal(t, "stays", obj.GetAnnotations()["external"],
		"external annotation should survive operator releasing ownership")

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-ann-delete")
	})
}

// TestReconciler_ExternalAnnotationInsertedMidLifecycle verifies that an external actor
// can add annotations at any point in the policy lifecycle and they persist across
// multiple reconciler re-applies.
func TestReconciler_ExternalAnnotationInsertedMidLifecycle(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	// Step 1: Apply policy with no annotations
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
				Id:   "cnp-ext-insert",
				Name: "e2e-ext-insert",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "insert-v1"},
								},
								Ingress: []*pb.CiliumPolicyIngressRule{{}},
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
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-ext-insert")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "policy should be applied")

	// Step 2: External actor adds annotation
	patch := []byte(`{"metadata":{"annotations":{"injected-by":"external-controller"}}}`)
	_, err := testClient.GetDynamicClient().Resource(ccnpGVR).Patch(
		ctx, "e2e-ext-insert", types.MergePatchType, patch, metav1.PatchOptions{},
	)
	require.NoError(t, err)

	// Step 3: Reconciler re-applies twice (two mutations) — external annotation should survive both
	for i, label := range []string{"insert-v2", "insert-v3"} {
		fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
			Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
				ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
					Mutation: &pb.ConfiguredKubernetesObjectMutation_UpdateObject{
						UpdateObject: &pb.ConfiguredKubernetesObjectData{
							Id:   "cnp-ext-insert",
							Name: "e2e-ext-insert",
							KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
								CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
									Specs: []*pb.CiliumPolicyRule{
										{
											EndpointSelector: &pb.LabelSelector{
												MatchLabels: map[string]string{"app": label},
											},
											Ingress: []*pb.CiliumPolicyIngressRule{{}},
										},
									},
								},
							},
						},
					},
				},
			},
		})

		expectedLabel := label
		require.Eventually(t, func() bool {
			obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-ext-insert")
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
			return ml["app"] == expectedLabel
		}, 20*time.Second, 100*time.Millisecond, "policy should be updated (mutation %d)", i+1)
	}

	// Verify external annotation survived both re-applies
	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-ext-insert")
	require.NoError(t, err)
	assert.Equal(t, "external-controller", obj.GetAnnotations()["injected-by"],
		"external annotation should survive multiple SSA re-applies")

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-ext-insert")
	})
}
