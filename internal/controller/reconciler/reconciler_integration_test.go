// Copyright 2026 Illumio, Inc. All Rights Reserved.

//go:build envtest

package reconciler

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/illumio/cloud-operator/internal/controller/logging"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/controller/stream/config"
	"github.com/illumio/cloud-operator/internal/controller/stream/config/cache"
	"github.com/illumio/cloud-operator/internal/controller/stream/resources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
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

func TestReconciler_BulkPopulatedSnapshot(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()
	boolTrue := true

	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})

	// First policy: detailed spec with enableDefaultDeny and custom labels
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

	// 5 more simple policies in the same snapshot
	bulkNames := []string{"bulk-1", "bulk-2", "bulk-3", "bulk-4", "bulk-5"}
	for _, name := range bulkNames {
		fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
			Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
				ResourceData: &pb.ConfiguredKubernetesObjectData{
					Id:   "bulk-" + name,
					Name: name,
					KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
						CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
							Specs: []*pb.CiliumPolicyRule{
								{
									EndpointSelector: &pb.LabelSelector{
										MatchLabels: map[string]string{"app": name},
									},
									Ingress: []*pb.CiliumPolicyIngressRule{{}},
								},
							},
						},
					},
				},
			},
		})
	}

	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	allNames := append([]string{"e2e-snapshot-policy"}, bulkNames...)

	// Wait for all 6 to appear
	require.Eventually(t, func() bool {
		for _, name := range allNames {
			obj, err := testClient.GetResource(ctx, ccnpGVR, "", name)
			if err != nil || obj == nil {
				return false
			}
		}
		return true
	}, 20*time.Second, 100*time.Millisecond, "all 6 policies should be applied")

	// Deep assertions on the detailed policy
	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-snapshot-policy")
	require.NoError(t, err)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())
	assert.Equal(t, "e2e-snapshot-policy", obj.GetName())
	assert.Equal(t, "e2e", obj.GetLabels()["env"])

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

	// Verify bulk policies have correct labels
	for _, name := range bulkNames {
		bulkObj, err := testClient.GetResource(ctx, ccnpGVR, "", name)
		require.NoError(t, err)

		assert.Equal(t, "bulk-"+name, bulkObj.GetLabels()[convert.CloudSecureIDLabel])
	}

	t.Cleanup(func() {
		for _, name := range allNames {
			_ = testClient.DeleteResource(ctx, ccnpGVR, "", name)
		}
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
		if mf.Manager == convert.FieldManager {
			found = true

			break
		}
	}

	assert.True(t, found, "expected field manager %q in managed fields", convert.FieldManager)

	assert.Equal(t, "cnp-ssa", obj.GetLabels()[convert.CloudSecureIDLabel])

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

	// Verify policy A has correct labels after update
	objA, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-multi-a")
	require.NoError(t, err)

	assert.Equal(t, "cnp-multi-a", objA.GetLabels()[convert.CloudSecureIDLabel])

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

	assert.Equal(t, "cnp-update-spec", obj.GetLabels()[convert.CloudSecureIDLabel])

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

	assert.Equal(t, "cidr-mut-1", obj.GetLabels()[convert.CloudSecureIDLabel])

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

	assert.Equal(t, "cidr-mut-1", obj.GetLabels()[convert.CloudSecureIDLabel])

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

	assert.Equal(t, "ccnp-clusterscope", obj.GetLabels()[convert.CloudSecureIDLabel])

	expected := loadExpectedPolicy(t, "clusterscope-policy.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "clusterwide-policy-example")
	})
}

// TestReconciler_CiliumExample_ServiceAccountPolicy tests service account-based policy selection.
// Source: https://github.com/cilium/cilium/blob/main/examples/policies/kubernetes/serviceaccount/serviceaccount-policy.yaml
// Adapted to CiliumClusterwideNetworkPolicy for envtest. L7 HTTP rules are omitted since the
// proto does not support them; only the L3 service account selectors and L4 port rule are tested.
func TestReconciler_CiliumExample_ServiceAccountPolicy(t *testing.T) {
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
				Id:   "ccnp-svc-account",
				Name: "k8s-svc-account",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{
										"io.cilium.k8s.policy.serviceaccount": "leia",
									},
								},
								Ingress: []*pb.CiliumPolicyIngressRule{
									{
										FromEndpoints: &pb.LabelSelectorList{
											Items: []*pb.LabelSelector{
												{MatchLabels: map[string]string{
													"io.cilium.k8s.policy.serviceaccount": "luke",
												}},
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
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "k8s-svc-account")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "service account policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "k8s-svc-account")
	require.NoError(t, err)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())

	assert.Equal(t, "ccnp-svc-account", obj.GetLabels()[convert.CloudSecureIDLabel])

	expected := loadExpectedPolicy(t, "serviceaccount-policy.yaml")
	assert.Equal(t, expected["spec"], obj.Object["spec"])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "k8s-svc-account")
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

	assert.Equal(t, "ccnp-wildcard", obj.GetLabels()[convert.CloudSecureIDLabel])

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

	assert.Equal(t, "ccnp-ext-lockdown", obj.GetLabels()[convert.CloudSecureIDLabel])

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

	assert.Equal(t, "ccnp-init", obj.GetLabels()[convert.CloudSecureIDLabel])

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

	assert.Equal(t, "ccnp-demo-host", obj.GetLabels()[convert.CloudSecureIDLabel])

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

	assert.Equal(t, "cnp-icmp", obj.GetLabels()[convert.CloudSecureIDLabel])

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

	assert.Equal(t, "cnp-cidr", obj.GetLabels()[convert.CloudSecureIDLabel])

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

	assert.Equal(t, "cnp-match-and", obj.GetLabels()[convert.CloudSecureIDLabel])

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

	assert.Equal(t, "cnp-ext-ann", obj.GetLabels()[convert.CloudSecureIDLabel])

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

	assert.Equal(t, "cnp-ann-del", obj.GetLabels()[convert.CloudSecureIDLabel])

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

// TestReconciler_SnapshotReplacementBulkDelete verifies that when a second snapshot
// arrives (after stream reconnection) with fewer objects than the first, the reconciler
// deletes objects that are no longer in the config.
func TestReconciler_SnapshotReplacementBulkDelete(t *testing.T) {
	configCache := cache.NewConfiguredObjectCache()
	runtimeCache := cache.NewConfiguredObjectCache()

	t.Cleanup(func() {
		configCache.Close()
		runtimeCache.Close()
	})

	h := newTestHarness(t)
	conn := h.DialGRPC(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	resourcesFactory := &resources.Factory{
		Logger:       zap.NewNop(),
		K8sClient:    testClient,
		Stats:        stream.NewStats(),
		RuntimeCache: runtimeCache,
		ConfigCache:  configCache,
	}
	resourcesClient, err := resourcesFactory.NewStreamClient(ctx, conn)
	require.NoError(t, err)

	go resourcesClient.Run(ctx)

	configFactory := &config.Factory{
		Logger:             zap.NewNop(),
		BufferedGrpcSyncer: logging.NewBufferedGrpcWriteSyncerForTest(zap.NewNop()),
		Stats:              stream.NewStats(),
		Cache:              configCache,
	}
	configClient, err := configFactory.NewStreamClient(ctx, conn)
	require.NoError(t, err)

	go configClient.Run(ctx)

	r := NewReconciler(zap.NewNop(), testClient, configCache, runtimeCache)
	go r.Run(ctx)

	fs := h.Server

	// First snapshot: A, B, C
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	for _, name := range []string{"snap-a", "snap-b", "snap-c"} {
		fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
			Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
				ResourceData: &pb.ConfiguredKubernetesObjectData{
					Id:   "snap-" + name,
					Name: name,
					KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
						CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
							Specs: []*pb.CiliumPolicyRule{
								{
									EndpointSelector: &pb.LabelSelector{
										MatchLabels: map[string]string{"app": name},
									},
									Ingress: []*pb.CiliumPolicyIngressRule{{}},
								},
							},
						},
					},
				},
			},
		})
	}
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	// Wait for all three to appear
	require.Eventually(t, func() bool {
		for _, name := range []string{"snap-a", "snap-b", "snap-c"} {
			obj, err := testClient.GetResource(ctx, ccnpGVR, "", name)
			if err != nil || obj == nil {
				return false
			}
		}
		return true
	}, 20*time.Second, 100*time.Millisecond, "all three policies should be applied")

	// Simulate stream disconnect and reconnect
	fs.DisconnectConfigStream()
	time.Sleep(200 * time.Millisecond)

	newConfigClient, err := configFactory.NewStreamClient(ctx, conn)
	require.NoError(t, err)

	go newConfigClient.Run(ctx)

	// Second snapshot (after reconnection): only A
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
				Id:   "snap-snap-a",
				Name: "snap-a",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "snap-a"},
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

	// B and C should be deleted
	require.Eventually(t, func() bool {
		_, errB := testClient.GetResource(ctx, ccnpGVR, "", "snap-b")
		_, errC := testClient.GetResource(ctx, ccnpGVR, "", "snap-c")
		return apierrors.IsNotFound(errB) && apierrors.IsNotFound(errC)
	}, 20*time.Second, 100*time.Millisecond, "snap-b and snap-c should be deleted after second snapshot")

	// A should still exist
	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "snap-a")
	require.NoError(t, err)
	assert.Equal(t, "snap-a", obj.GetName())

	assert.Equal(t, "snap-snap-a", obj.GetLabels()[convert.CloudSecureIDLabel])

	t.Cleanup(func() {
		for _, name := range []string{"snap-a", "snap-b", "snap-c"} {
			_ = testClient.DeleteResource(ctx, ccnpGVR, "", name)
		}
	})
}

// TestReconciler_IdempotentReApply verifies that sending the same policy twice
// (duplicate create mutation) does not cause errors — the reconciler applies
// idempotently via SSA.
func TestReconciler_IdempotentReApply(t *testing.T) {
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

	policyData := &pb.ConfiguredKubernetesObjectData{
		Id:   "cnp-idempotent",
		Name: "e2e-idempotent",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
			CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{
						EndpointSelector: &pb.LabelSelector{
							MatchLabels: map[string]string{"app": "idempotent"},
						},
						Ingress: []*pb.CiliumPolicyIngressRule{{}},
					},
				},
			},
		},
	}

	// Send same create mutation twice
	for range 2 {
		fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
			Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
				ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
					Mutation: &pb.ConfiguredKubernetesObjectMutation_CreateObject{
						CreateObject: policyData,
					},
				},
			},
		})
	}

	// Wait for the policy to appear
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-idempotent")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "idempotent policy should be applied")

	// Give time for the second mutation to be processed
	time.Sleep(1 * time.Second)

	// Verify the policy still exists and is correct (no error from duplicate apply)
	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-idempotent")
	require.NoError(t, err)
	assert.Equal(t, "e2e-idempotent", obj.GetName())

	assert.Equal(t, "cnp-idempotent", obj.GetLabels()[convert.CloudSecureIDLabel])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-idempotent")
	})
}

// TestReconciler_MixedKindsInSnapshot verifies that a single snapshot can contain
// multiple resource kinds (CCNP, CNP, CIDRGroup) and all are applied correctly.
// Reuses the same proto structures as the individual Cilium example tests.
func TestReconciler_MixedKindsInSnapshot(t *testing.T) {
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

	// CCNP — same policy as TestReconciler_CiliumExample_ClusterscopePolicy (luke→leia)
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "mixed-ccnp",
				Name: "mixed-clusterwide-policy",
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

	// CNP — same policy as TestReconciler_CiliumExample_NamespaceLabelsPolicy
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:        "mixed-cnp",
				Name:      "mixed-namespace-policy",
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

	// CIDRGroup — same structure as TestReconciler_CIDRGroupMutationCreateUpdateDelete
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "mixed-cidr",
				Name: "mixed-cidr-group",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumCidrGroup{
					CiliumCidrGroup: &pb.KubernetesCiliumCIDRGroupData{
						Spec: &pb.CiliumCIDRGroup{
							ExternalCidrs: []string{"10.0.0.0/8"},
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

	// Wait for all three kinds to appear
	require.Eventually(t, func() bool {
		ccnp, err1 := testClient.GetResource(ctx, ccnpGVR, "", "mixed-clusterwide-policy")
		cnp, err2 := testClient.GetResource(ctx, cnpGVR, ns, "mixed-namespace-policy")
		cidr, err3 := testClient.GetResource(ctx, cidrGroupGVR, "", "mixed-cidr-group")
		return err1 == nil && ccnp != nil && err2 == nil && cnp != nil && err3 == nil && cidr != nil
	}, 20*time.Second, 100*time.Millisecond, "all three resource kinds should be applied")

	// Verify CCNP spec matches the clusterscope-policy fixture
	ccnpObj, err := testClient.GetResource(ctx, ccnpGVR, "", "mixed-clusterwide-policy")
	require.NoError(t, err)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", ccnpObj.GetKind())

	assert.Equal(t, "mixed-ccnp", ccnpObj.GetLabels()[convert.CloudSecureIDLabel])
	expectedCCNP := loadExpectedPolicy(t, "clusterscope-policy.yaml")
	assert.Equal(t, expectedCCNP["spec"], ccnpObj.Object["spec"])

	// Verify CNP spec matches the namespace-labels-policy fixture
	cnpObj, err := testClient.GetResource(ctx, cnpGVR, ns, "mixed-namespace-policy")
	require.NoError(t, err)
	assert.Equal(t, "CiliumNetworkPolicy", cnpObj.GetKind())

	assert.Equal(t, "mixed-cnp", cnpObj.GetLabels()[convert.CloudSecureIDLabel])
	expectedCNP := loadExpectedPolicy(t, "namespace-labels-policy.yaml")
	assert.Equal(t, expectedCNP["spec"], cnpObj.Object["spec"])

	// Verify CIDRGroup
	cidrObj, err := testClient.GetResource(ctx, cidrGroupGVR, "", "mixed-cidr-group")
	require.NoError(t, err)
	assert.Equal(t, "CiliumCIDRGroup", cidrObj.GetKind())

	assert.Equal(t, "mixed-cidr", cidrObj.GetLabels()[convert.CloudSecureIDLabel])
	spec, ok := cidrObj.Object["spec"].(map[string]any)
	require.True(t, ok)
	cidrs, ok := spec["externalCIDRs"].([]any)
	require.True(t, ok)
	require.Len(t, cidrs, 1)
	assert.Equal(t, "10.0.0.0/8", cidrs[0])

	// --- Mixed mutations: update CCNP, delete CNP, update CIDRGroup ---

	// Update CCNP: change endpoint selector from leia to han
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_UpdateObject{
					UpdateObject: &pb.ConfiguredKubernetesObjectData{
						Id:   "mixed-ccnp",
						Name: "mixed-clusterwide-policy",
						KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
							CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
								Specs: []*pb.CiliumPolicyRule{
									{
										EndpointSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"name": "han"},
										},
										Ingress: []*pb.CiliumPolicyIngressRule{
											{
												FromEndpoints: &pb.LabelSelectorList{
													Items: []*pb.LabelSelector{
														{MatchLabels: map[string]string{"name": "chewie"}},
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

	// Delete CNP
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_DeleteObject{
					DeleteObject: &pb.DeleteConfiguredKubernetesObject{
						Id: "mixed-cnp",
					},
				},
			},
		},
	})

	// Update CIDRGroup: add another CIDR
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_UpdateObject{
					UpdateObject: &pb.ConfiguredKubernetesObjectData{
						Id:   "mixed-cidr",
						Name: "mixed-cidr-group",
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

	// Verify CCNP was updated
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "mixed-clusterwide-policy")
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
		return ml["name"] == "han"
	}, 20*time.Second, 100*time.Millisecond, "CCNP should be updated to han/chewie")

	// Verify CNP was deleted
	require.Eventually(t, func() bool {
		_, err := testClient.GetResource(ctx, cnpGVR, ns, "mixed-namespace-policy")
		return apierrors.IsNotFound(err)
	}, 20*time.Second, 100*time.Millisecond, "CNP should be deleted")

	// Verify post-mutation CCNP labels
	updatedCCNP, err := testClient.GetResource(ctx, ccnpGVR, "", "mixed-clusterwide-policy")
	require.NoError(t, err)

	assert.Equal(t, "mixed-ccnp", updatedCCNP.GetLabels()[convert.CloudSecureIDLabel])

	// Verify CIDRGroup was updated with two CIDRs
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, cidrGroupGVR, "", "mixed-cidr-group")
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
	}, 20*time.Second, 100*time.Millisecond, "CIDRGroup should be updated with two CIDRs")

	// Verify post-mutation CIDRGroup labels
	updatedCIDR, err := testClient.GetResource(ctx, cidrGroupGVR, "", "mixed-cidr-group")
	require.NoError(t, err)

	assert.Equal(t, "mixed-cidr", updatedCIDR.GetLabels()[convert.CloudSecureIDLabel])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "mixed-clusterwide-policy")
		_ = testClient.DeleteResource(ctx, cnpGVR, ns, "mixed-namespace-policy")
		_ = testClient.DeleteResource(ctx, cidrGroupGVR, "", "mixed-cidr-group")
	})
}

// TestReconciler_DeleteNonExistent verifies that a delete mutation for an ID that
// was never created does not cause an error — the reconciler handles it gracefully.
func TestReconciler_DeleteNonExistent(t *testing.T) {
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

	// Delete an object that was never created
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_DeleteObject{
					DeleteObject: &pb.DeleteConfiguredKubernetesObject{
						Id: "cnp-ghost",
					},
				},
			},
		},
	})

	// Give time for the mutation to be processed
	time.Sleep(1 * time.Second)

	// Send a create mutation after to verify the pipeline is still healthy
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_CreateObject{
					CreateObject: &pb.ConfiguredKubernetesObjectData{
						Id:   "cnp-after-ghost",
						Name: "e2e-after-ghost",
						KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
							CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
								Specs: []*pb.CiliumPolicyRule{
									{
										EndpointSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"app": "alive"},
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

	// The subsequent create should succeed, proving the pipeline wasn't broken
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-after-ghost")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "policy after ghost delete should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-after-ghost")
	require.NoError(t, err)

	assert.Equal(t, "cnp-after-ghost", obj.GetLabels()[convert.CloudSecureIDLabel])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-after-ghost")
	})
}

// TestReconciler_PoliciesPersistAfterOperatorShutdown verifies that when the operator
// is stopped (simulated by context cancelled), all applied policies remain in Kubernetes. This
// simulates operator uninstall/redeployment — policies must persist to avoid leaving
// the cluster unsecured.
func TestReconciler_PoliciesPersistAfterOperatorShutdown(t *testing.T) {
	// Wire the pipeline manually so we control the context lifecycle
	configCache := cache.NewConfiguredObjectCache()
	runtimeCache := cache.NewConfiguredObjectCache()

	t.Cleanup(func() {
		configCache.Close()
		runtimeCache.Close()
	})

	h := newTestHarness(t)
	conn := h.DialGRPC(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resourcesFactory := &resources.Factory{
		Logger:       zap.NewNop(),
		K8sClient:    testClient,
		Stats:        stream.NewStats(),
		RuntimeCache: runtimeCache,
		ConfigCache:  configCache,
	}
	resourcesClient, err := resourcesFactory.NewStreamClient(ctx, conn)
	require.NoError(t, err)

	go resourcesClient.Run(ctx)

	configFactory := &config.Factory{
		Logger:             zap.NewNop(),
		BufferedGrpcSyncer: logging.NewBufferedGrpcWriteSyncerForTest(zap.NewNop()),
		Stats:              stream.NewStats(),
		Cache:              configCache,
	}
	configClient, err := configFactory.NewStreamClient(ctx, conn)
	require.NoError(t, err)

	go configClient.Run(ctx)

	r := NewReconciler(zap.NewNop(), testClient, configCache, runtimeCache)
	go r.Run(ctx)

	fs := h.Server

	// Apply two policies
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
				Id:   "persist-ccnp",
				Name: "e2e-persist-ccnp",
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
			},
		},
	})
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:        "persist-cnp",
				Name:      "e2e-persist-cnp",
				Namespace: &ns,
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
					CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"name": "rebel-base"},
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

	// Wait for both policies to be applied
	require.Eventually(t, func() bool {
		ccnp, err1 := testClient.GetResource(ctx, ccnpGVR, "", "e2e-persist-ccnp")
		cnp, err2 := testClient.GetResource(ctx, cnpGVR, ns, "e2e-persist-cnp")
		return err1 == nil && ccnp != nil && err2 == nil && cnp != nil
	}, 20*time.Second, 100*time.Millisecond, "both policies should be applied before shutdown")

	// Simulate operator shutdown — cancel the context, stopping all goroutines
	cancel()

	// Give goroutines time to exit
	time.Sleep(500 * time.Millisecond)

	// Policies must still exist in K8s after operator shutdown
	freshCtx := context.Background()

	ccnpObj, err := testClient.GetResource(freshCtx, ccnpGVR, "", "e2e-persist-ccnp")
	require.NoError(t, err, "CCNP should persist after operator shutdown")
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", ccnpObj.GetKind())

	assert.Equal(t, "persist-ccnp", ccnpObj.GetLabels()[convert.CloudSecureIDLabel])

	cnpObj, err := testClient.GetResource(freshCtx, cnpGVR, ns, "e2e-persist-cnp")
	require.NoError(t, err, "CNP should persist after operator shutdown")
	assert.Equal(t, "CiliumNetworkPolicy", cnpObj.GetKind())

	assert.Equal(t, "persist-cnp", cnpObj.GetLabels()[convert.CloudSecureIDLabel])

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_ = testClient.DeleteResource(cleanupCtx, ccnpGVR, "", "e2e-persist-ccnp")
		_ = testClient.DeleteResource(cleanupCtx, cnpGVR, ns, "e2e-persist-cnp")
	})
}

// TestReconciler_EmptySnapshotDeletesAll verifies that when a stream reconnects and sends
// an empty snapshot (no policies), all previously applied managed resources are deleted.
// This simulates the production reconnection flow: stream disconnect → new stream client →
// fresh snapshot with no ResourceData → ReplaceAll({}) empties config cache → reconciler deletes.
func TestReconciler_EmptySnapshotDeletesAll(t *testing.T) {
	configCache := cache.NewConfiguredObjectCache()
	runtimeCache := cache.NewConfiguredObjectCache()

	t.Cleanup(func() {
		configCache.Close()
		runtimeCache.Close()
	})

	h := newTestHarness(t)
	conn := h.DialGRPC(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	// Start resources stream client
	resourcesFactory := &resources.Factory{
		Logger:       zap.NewNop(),
		K8sClient:    testClient,
		Stats:        stream.NewStats(),
		RuntimeCache: runtimeCache,
		ConfigCache:  configCache,
	}
	resourcesClient, err := resourcesFactory.NewStreamClient(ctx, conn)
	require.NoError(t, err)

	go resourcesClient.Run(ctx)

	// Start config stream client
	configFactory := &config.Factory{
		Logger:             zap.NewNop(),
		BufferedGrpcSyncer: logging.NewBufferedGrpcWriteSyncerForTest(zap.NewNop()),
		Stats:              stream.NewStats(),
		Cache:              configCache,
	}
	configClient, err := configFactory.NewStreamClient(ctx, conn)
	require.NoError(t, err)

	go configClient.Run(ctx)

	// Start reconciler
	r := NewReconciler(zap.NewNop(), testClient, configCache, runtimeCache)
	go r.Run(ctx)

	fs := h.Server

	// First snapshot: two policies
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	})
	for _, name := range []string{"empty-snap-a", "empty-snap-b"} {
		fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
			Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
				ResourceData: &pb.ConfiguredKubernetesObjectData{
					Id:   "es-" + name,
					Name: name,
					KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
						CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
							Specs: []*pb.CiliumPolicyRule{
								{
									EndpointSelector: &pb.LabelSelector{
										MatchLabels: map[string]string{"app": name},
									},
									Ingress: []*pb.CiliumPolicyIngressRule{{}},
								},
							},
						},
					},
				},
			},
		})
	}
	fs.SendConfigResponse(&pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	})

	// Wait for both to appear
	require.Eventually(t, func() bool {
		a, err1 := testClient.GetResource(ctx, ccnpGVR, "", "empty-snap-a")
		b, err2 := testClient.GetResource(ctx, ccnpGVR, "", "empty-snap-b")
		return err1 == nil && a != nil && err2 == nil && b != nil
	}, 20*time.Second, 100*time.Millisecond, "both policies should be applied")

	// Simulate stream disconnect: close the config channel so the current config client
	// sees EOF and its Run() exits. A new channel is created for the next client.
	fs.DisconnectConfigStream()
	time.Sleep(200 * time.Millisecond)

	// Reconnect: create a new config stream client (fresh snapshotComplete=false)
	newConfigClient, err := configFactory.NewStreamClient(ctx, conn)
	require.NoError(t, err)

	go newConfigClient.Run(ctx)

	// Second snapshot: empty (no ResourceData, just snapshot complete)
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

	// Both should be deleted
	require.Eventually(t, func() bool {
		_, errA := testClient.GetResource(ctx, ccnpGVR, "", "empty-snap-a")
		_, errB := testClient.GetResource(ctx, ccnpGVR, "", "empty-snap-b")
		return apierrors.IsNotFound(errA) && apierrors.IsNotFound(errB)
	}, 20*time.Second, 100*time.Millisecond, "empty snapshot should delete all managed resources")

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "empty-snap-a")
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "empty-snap-b")
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

	assert.Equal(t, "cnp-ext-insert", obj.GetLabels()[convert.CloudSecureIDLabel])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-ext-insert")
	})
}

// Reconciliation self-healing scenarios

// TestReconciler_SSAOverwritesExternalSpecMutation verifies that when an external agent
// (different field manager) mutates a spec field owned by the operator, the next
// reconciliation overwrites the external change back to the desired CloudSecure state.
func TestReconciler_SSAOverwritesExternalSpecMutation(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	// Apply policy via snapshot
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
				Id:   "cnp-ssa-overwrite",
				Name: "e2e-ssa-overwrite",
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
												{MatchLabels: map[string]string{"role": "frontend"}},
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
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-ssa-overwrite")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "policy should be applied")

	// External agent mutates the spec with a different field manager
	externalPatch := []byte(`{"spec":{"endpointSelector":{"matchLabels":{"app":"HACKED"}}}}`)
	_, err := testClient.GetDynamicClient().Resource(ccnpGVR).Patch(
		ctx, "e2e-ssa-overwrite", types.MergePatchType, externalPatch, metav1.PatchOptions{
			FieldManager: "external-agent",
		},
	)
	require.NoError(t, err)

	// The runtime watcher detects the spec change, updates the runtime cache,
	// and the reconciler automatically re-applies because config != runtime.
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-ssa-overwrite")
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
		return ml["app"] == "web"
	}, 20*time.Second, 100*time.Millisecond, "operator should overwrite external spec mutation")

	// Verify operator's field manager owns the spec
	raw, err := testClient.GetDynamicClient().Resource(ccnpGVR).Get(ctx, "e2e-ssa-overwrite", metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, "cnp-ssa-overwrite", raw.GetLabels()[convert.CloudSecureIDLabel])

	var operatorOwnsSpec bool
	for _, mf := range raw.GetManagedFields() {
		if mf.Manager == convert.FieldManager {
			operatorOwnsSpec = true

			break
		}
	}

	assert.True(t, operatorOwnsSpec, "operator field manager should own the spec after overwrite")

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-ssa-overwrite")
	})
}

// TestReconciler_DeletedPolicyRestoredByReconciler verifies that when a managed policy
// is accidentally deleted from Kubernetes (e.g. by kubectl or another controller), the
// reconciler detects the drift via the runtime cache and re-applies the policy from the
// config cache on the next reconciliation cycle.
func TestReconciler_DeletedPolicyRestoredByReconciler(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	// Apply policy via snapshot
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
				Id:   "cnp-restore",
				Name: "e2e-restore-policy",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "critical"},
								},
								Ingress: []*pb.CiliumPolicyIngressRule{
									{
										FromEndpoints: &pb.LabelSelectorList{
											Items: []*pb.LabelSelector{
												{MatchLabels: map[string]string{"role": "trusted"}},
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

	// Wait for the policy to appear
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-restore-policy")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-restore-policy")
	require.NoError(t, err)

	assert.Equal(t, "cnp-restore", obj.GetLabels()[convert.CloudSecureIDLabel])

	// Simulate accidental deletion (e.g. kubectl delete)
	err = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-restore-policy")
	require.NoError(t, err)

	// Verify it's gone
	_, err = testClient.GetResource(ctx, ccnpGVR, "", "e2e-restore-policy")
	require.True(t, apierrors.IsNotFound(err), "policy should be deleted")

	// The runtime watcher should detect the deletion, update the runtime cache,
	// and the reconciler should re-apply the policy from the config cache.
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-restore-policy")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "policy should be restored by reconciler after accidental deletion")

	// Verify the restored policy has the correct spec and labels
	restored, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-restore-policy")
	require.NoError(t, err)

	assert.Equal(t, "cnp-restore", restored.GetLabels()[convert.CloudSecureIDLabel])

	spec, ok := restored.Object["spec"].(map[string]any)
	require.True(t, ok)
	es, ok := spec["endpointSelector"].(map[string]any)
	require.True(t, ok)
	ml, ok := es["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "critical", ml["app"], "restored policy should have the original spec")

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-restore-policy")
	})
}

// TestReconciler_CloudSecureIDLabelStripped verifies that when an external actor strips the
// CloudSecureIDLabel from an operator-managed policy, the reconciler self-heals.
//
// The runtime watcher detects the change via field manager ownership (not labels) and uses
// the config cache reverse lookup to find the object's ID by name/kind/namespace.
// The reconciler sees config != runtime and re-applies via SSA, restoring the label.
func TestReconciler_CloudSecureIDLabelStripped(t *testing.T) {
	fs := setupSuite(t)

	ctx := context.Background()

	// Apply policy via snapshot
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
				Id:   "cnp-id-stripped",
				Name: "e2e-id-stripped",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "labeled"},
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
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-id-stripped")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-id-stripped")
	require.NoError(t, err)
	assert.Equal(t, "cnp-id-stripped", obj.GetLabels()[convert.CloudSecureIDLabel])

	// External actor strips the CloudSecureIDLabel
	patch := []byte(`{"metadata":{"labels":{"cloud.illum.io/resource-id":null}}}`)
	_, err = testClient.GetDynamicClient().Resource(ccnpGVR).Patch(
		ctx, "e2e-id-stripped", types.MergePatchType, patch, metav1.PatchOptions{},
	)
	require.NoError(t, err)

	// Verify the label was actually removed
	obj, err = testClient.GetResource(ctx, ccnpGVR, "", "e2e-id-stripped")
	require.NoError(t, err)
	assert.Empty(t, obj.GetLabels()[convert.CloudSecureIDLabel], "cloudsecure-id label should be removed")

	// The runtime watcher detects the change via field manager ownership, uses
	// config cache reverse lookup to find the ID, and updates the runtime cache.
	// The reconciler sees config != runtime and re-applies via SSA.
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-id-stripped")
		if err != nil || obj == nil {
			return false
		}
		return obj.GetLabels()[convert.CloudSecureIDLabel] == "cnp-id-stripped"
	}, 20*time.Second, 100*time.Millisecond, "cloudsecure-id label should be self-healed by reconciler")

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-id-stripped")
	})
}

// TestReconciler_CloudSecureIDLabelMutated verifies that when an external agent overwrites
// the cloudsecure-id label, the reconciler restores it via SSA. The runtime watcher detects
// the change via field manager ownership, the runtime cache updates with the wrong ID, and
// the reconciler detects config != runtime and re-applies.
func TestReconciler_CloudSecureIDLabelMutated(t *testing.T) {
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
				Id:   "cnp-id-mutated",
				Name: "e2e-id-mutated",
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
						Specs: []*pb.CiliumPolicyRule{
							{
								EndpointSelector: &pb.LabelSelector{
									MatchLabels: map[string]string{"app": "id-test"},
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
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-id-mutated")
		return err == nil && obj != nil
	}, 20*time.Second, 100*time.Millisecond, "policy should be applied")

	obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-id-mutated")
	require.NoError(t, err)
	assert.Equal(t, "cnp-id-mutated", obj.GetLabels()[convert.CloudSecureIDLabel])

	// External agent overwrites the cloudsecure-id label
	patch := []byte(`{"metadata":{"labels":{"` + convert.CloudSecureIDLabel + `":"WRONG-ID"}}}`)
	_, err = testClient.GetDynamicClient().Resource(ccnpGVR).Patch(
		ctx, "e2e-id-mutated", types.MergePatchType, patch, metav1.PatchOptions{},
	)
	require.NoError(t, err)

	// Verify the label was mutated
	obj, err = testClient.GetResource(ctx, ccnpGVR, "", "e2e-id-mutated")
	require.NoError(t, err)
	assert.Equal(t, "WRONG-ID", obj.GetLabels()[convert.CloudSecureIDLabel])

	// The runtime watcher detects the change via field manager ownership.
	// The runtime cache updates, the reconciler detects config != runtime, and re-applies.
	require.Eventually(t, func() bool {
		obj, err := testClient.GetResource(ctx, ccnpGVR, "", "e2e-id-mutated")
		if err != nil || obj == nil {
			return false
		}
		return obj.GetLabels()[convert.CloudSecureIDLabel] == "cnp-id-mutated"
	}, 20*time.Second, 100*time.Millisecond, "cloudsecure-id label should be restored by reconciler")

	obj, err = testClient.GetResource(ctx, ccnpGVR, "", "e2e-id-mutated")
	require.NoError(t, err)

	assert.Equal(t, "cnp-id-mutated", obj.GetLabels()[convert.CloudSecureIDLabel])

	t.Cleanup(func() {
		_ = testClient.DeleteResource(ctx, ccnpGVR, "", "e2e-id-mutated")
	})
}
