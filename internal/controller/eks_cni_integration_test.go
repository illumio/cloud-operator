// Copyright 2026 Illumio, Inc. All Rights Reserved.

//go:build integration

package controller

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	eksIntegrationClusterName = "eks-cni-integration-test"
	testNamespace             = "test-namespace"
)

// EKSCNIIntegrationSuite tests EKS CNI resource handling with a real kind cluster.
type EKSCNIIntegrationSuite struct {
	suite.Suite
	dynamicClient dynamic.Interface
	ctx           context.Context
	cancel        context.CancelFunc
}

func TestEKSCNIIntegrationSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	suite.Run(t, new(EKSCNIIntegrationSuite))
}

func (s *EKSCNIIntegrationSuite) SetupSuite() {
	s.ctx, s.cancel = context.WithTimeout(context.Background(), 5*time.Minute)

	// Create kind cluster
	s.T().Log("Creating kind cluster...")
	err := s.createKindCluster()
	require.NoError(s.T(), err, "Failed to create kind cluster")

	// Wait for cluster to be ready
	s.T().Log("Waiting for cluster to be ready...")
	err = s.waitForClusterReady()
	require.NoError(s.T(), err, "Cluster not ready")

	// Create dynamic client
	s.T().Log("Creating Kubernetes client...")
	s.dynamicClient, err = s.createDynamicClient()
	require.NoError(s.T(), err, "Failed to create dynamic client")

	// Apply CRDs
	s.T().Log("Applying CRDs...")
	err = s.applyCRDs()
	require.NoError(s.T(), err, "Failed to apply CRDs")

	// Wait for CRDs to be established
	time.Sleep(2 * time.Second)

	// Create test namespace
	s.T().Log("Creating test namespace...")
	err = s.createTestNamespace()
	require.NoError(s.T(), err, "Failed to create test namespace")
}

func (s *EKSCNIIntegrationSuite) TearDownSuite() {
	s.cancel()
	s.T().Log("Deleting kind cluster...")
	_ = s.deleteKindCluster()
}

func (s *EKSCNIIntegrationSuite) createKindCluster() error {
	// Check if cluster already exists
	checkCmd := exec.CommandContext(s.ctx, "kind", "get", "clusters")
	output, _ := checkCmd.Output()
	if string(output) != "" {
		// Delete existing cluster with same name
		_ = exec.CommandContext(s.ctx, "kind", "delete", "cluster", "--name", eksIntegrationClusterName).Run()
	}

	cmd := exec.CommandContext(s.ctx, "kind", "create", "cluster", "--name", eksIntegrationClusterName, "--wait", "60s")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (s *EKSCNIIntegrationSuite) deleteKindCluster() error {
	cmd := exec.CommandContext(context.Background(), "kind", "delete", "cluster", "--name", eksIntegrationClusterName)
	return cmd.Run()
}

func (s *EKSCNIIntegrationSuite) waitForClusterReady() error {
	cmd := exec.CommandContext(s.ctx, "kubectl", "cluster-info", "--context", "kind-"+eksIntegrationClusterName)
	return cmd.Run()
}

func (s *EKSCNIIntegrationSuite) createDynamicClient() (dynamic.Interface, error) {
	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}
	return dynamic.NewForConfig(config)
}

func (s *EKSCNIIntegrationSuite) applyCRDs() error {
	crdDir := "testdata/eks-cni/crds"
	files, err := os.ReadDir(crdDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if filepath.Ext(file.Name()) == ".yaml" {
			cmd := exec.CommandContext(s.ctx, "kubectl", "apply", "-f",
				filepath.Join(crdDir, file.Name()),
				"--context", "kind-"+eksIntegrationClusterName)
			if err := cmd.Run(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *EKSCNIIntegrationSuite) createTestNamespace() error {
	cmd := exec.CommandContext(s.ctx, "kubectl", "create", "namespace", testNamespace,
		"--context", "kind-"+eksIntegrationClusterName)
	// Ignore error if namespace already exists
	_ = cmd.Run()

	// Label the namespace
	labelCmd := exec.CommandContext(s.ctx, "kubectl", "label", "namespace", testNamespace,
		"kubernetes.io/metadata.name="+testNamespace, "--overwrite",
		"--context", "kind-"+eksIntegrationClusterName)
	return labelCmd.Run()
}

// Test creating and retrieving ClusterNetworkPolicy resources.
func (s *EKSCNIIntegrationSuite) TestClusterNetworkPolicy_CreateAndConvert() {
	gvr := schema.GroupVersionResource{
		Group:    "networking.k8s.aws",
		Version:  "v1alpha1",
		Resource: "clusternetworkpolicies",
	}

	// Create a ClusterNetworkPolicy
	cnp := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "networking.k8s.aws/v1alpha1",
			"kind":       "ClusterNetworkPolicy",
			"metadata": map[string]any{
				"name": "integration-test-cnp",
				"labels": map[string]any{
					"test": "integration",
				},
			},
			"spec": map[string]any{
				"priority": int64(100),
				"tier":     "standard",
				"subject": map[string]any{
					"pods": map[string]any{
						"namespaceSelector": map[string]any{
							"matchLabels": map[string]any{
								"kubernetes.io/metadata.name": testNamespace,
							},
						},
						"podSelector": map[string]any{
							"matchLabels": map[string]any{
								"app": "web",
							},
						},
					},
				},
				"ingress": []any{
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
						"from": []any{
							map[string]any{
								"networks": []any{"10.0.0.0/8"},
							},
						},
					},
				},
				"egress": []any{
					map[string]any{
						"action": "Accept",
						"to": []any{
							map[string]any{
								"domainNames": []any{"api.example.com"},
							},
						},
					},
				},
			},
		},
	}

	// Create the resource
	created, err := s.dynamicClient.Resource(gvr).Create(s.ctx, cnp, metav1.CreateOptions{})
	require.NoError(s.T(), err, "Failed to create ClusterNetworkPolicy")
	assert.Equal(s.T(), "integration-test-cnp", created.GetName())

	// Retrieve the resource
	retrieved, err := s.dynamicClient.Resource(gvr).Get(s.ctx, "integration-test-cnp", metav1.GetOptions{})
	require.NoError(s.T(), err, "Failed to get ClusterNetworkPolicy")

	// Test conversion
	converted, err := ConvertUnstructuredToAWSNetworkPolicy(retrieved)
	require.NoError(s.T(), err, "Failed to convert ClusterNetworkPolicy")
	require.NotNil(s.T(), converted)
	assert.Equal(s.T(), "ClusterNetworkPolicy", converted.Kind)
	assert.Equal(s.T(), "integration-test-cnp", converted.Name)
	assert.Equal(s.T(), "integration", converted.Labels["test"])

	// Verify kind-specific data
	cnpData := converted.GetAwsClusterNetworkPolicy()
	require.NotNil(s.T(), cnpData, "AWS ClusterNetworkPolicy data should not be nil")
	assert.Equal(s.T(), int32(100), cnpData.Priority)
	assert.Equal(s.T(), "standard", cnpData.Tier)

	// Verify subject
	require.NotNil(s.T(), cnpData.Subject)
	require.NotNil(s.T(), cnpData.Subject.Pods)
	assert.Equal(s.T(), testNamespace, cnpData.Subject.Pods.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"])
	assert.Equal(s.T(), "web", cnpData.Subject.Pods.PodSelector.MatchLabels["app"])

	// Verify ingress rules
	require.Len(s.T(), cnpData.IngressRules, 1)
	assert.Equal(s.T(), "Accept", cnpData.IngressRules[0].Action)
	require.Len(s.T(), cnpData.IngressRules[0].Ports, 1)
	assert.Equal(s.T(), int32(80), cnpData.IngressRules[0].Ports[0].PortNumber.Port)
	require.Len(s.T(), cnpData.IngressRules[0].From, 1)
	assert.Contains(s.T(), cnpData.IngressRules[0].From[0].Networks, "10.0.0.0/8")

	// Verify egress rules with domain names
	require.Len(s.T(), cnpData.EgressRules, 1)
	assert.Equal(s.T(), "Accept", cnpData.EgressRules[0].Action)
	require.Len(s.T(), cnpData.EgressRules[0].To, 1)
	assert.Contains(s.T(), cnpData.EgressRules[0].To[0].DomainNames, "api.example.com")

	// Cleanup
	err = s.dynamicClient.Resource(gvr).Delete(s.ctx, "integration-test-cnp", metav1.DeleteOptions{})
	assert.NoError(s.T(), err, "Failed to delete ClusterNetworkPolicy")
}

// Test creating and retrieving SecurityGroupPolicy resources.
func (s *EKSCNIIntegrationSuite) TestSecurityGroupPolicy_CreateAndConvert() {
	gvr := schema.GroupVersionResource{
		Group:    "vpcresources.k8s.aws",
		Version:  "v1beta1",
		Resource: "securitygrouppolicies",
	}

	// Create a SecurityGroupPolicy
	sgp := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "vpcresources.k8s.aws/v1beta1",
			"kind":       "SecurityGroupPolicy",
			"metadata": map[string]any{
				"name":      "integration-test-sgp",
				"namespace": testNamespace,
				"labels": map[string]any{
					"test": "integration",
				},
			},
			"spec": map[string]any{
				"podSelector": map[string]any{
					"matchLabels": map[string]any{
						"app":  "database",
						"tier": "backend",
					},
				},
				"securityGroups": map[string]any{
					"groupIds": []any{
						"sg-0123456789abcdef0",
						"sg-0fedcba9876543210",
					},
				},
			},
		},
	}

	// Create the resource
	created, err := s.dynamicClient.Resource(gvr).Namespace(testNamespace).Create(s.ctx, sgp, metav1.CreateOptions{})
	require.NoError(s.T(), err, "Failed to create SecurityGroupPolicy")
	assert.Equal(s.T(), "integration-test-sgp", created.GetName())

	// Retrieve the resource
	retrieved, err := s.dynamicClient.Resource(gvr).Namespace(testNamespace).Get(s.ctx, "integration-test-sgp", metav1.GetOptions{})
	require.NoError(s.T(), err, "Failed to get SecurityGroupPolicy")

	// Test conversion
	converted, err := ConvertUnstructuredToAWSNetworkPolicy(retrieved)
	require.NoError(s.T(), err, "Failed to convert SecurityGroupPolicy")
	require.NotNil(s.T(), converted)
	assert.Equal(s.T(), "SecurityGroupPolicy", converted.Kind)
	assert.Equal(s.T(), "integration-test-sgp", converted.Name)
	assert.Equal(s.T(), testNamespace, converted.Namespace)
	assert.Equal(s.T(), "integration", converted.Labels["test"])

	// Verify kind-specific data
	sgpData := converted.GetAwsSecurityGroupPolicy()
	require.NotNil(s.T(), sgpData, "AWS SecurityGroupPolicy data should not be nil")

	// Verify pod selector
	require.NotNil(s.T(), sgpData.PodSelector)
	assert.Equal(s.T(), "database", sgpData.PodSelector.MatchLabels["app"])
	assert.Equal(s.T(), "backend", sgpData.PodSelector.MatchLabels["tier"])

	// Verify security group IDs
	require.Len(s.T(), sgpData.SecurityGroupIds, 2)
	assert.Contains(s.T(), sgpData.SecurityGroupIds, "sg-0123456789abcdef0")
	assert.Contains(s.T(), sgpData.SecurityGroupIds, "sg-0fedcba9876543210")

	// Cleanup
	err = s.dynamicClient.Resource(gvr).Namespace(testNamespace).Delete(s.ctx, "integration-test-sgp", metav1.DeleteOptions{})
	assert.NoError(s.T(), err, "Failed to delete SecurityGroupPolicy")
}

// Test SecurityGroupPolicy with serviceAccountSelector.
func (s *EKSCNIIntegrationSuite) TestSecurityGroupPolicy_ServiceAccountSelector() {
	gvr := schema.GroupVersionResource{
		Group:    "vpcresources.k8s.aws",
		Version:  "v1beta1",
		Resource: "securitygrouppolicies",
	}

	// Create a SecurityGroupPolicy with serviceAccountSelector
	sgp := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "vpcresources.k8s.aws/v1beta1",
			"kind":       "SecurityGroupPolicy",
			"metadata": map[string]any{
				"name":      "integration-test-sgp-sa",
				"namespace": testNamespace,
			},
			"spec": map[string]any{
				"serviceAccountSelector": map[string]any{
					"matchLabels": map[string]any{
						"role": "external-access",
					},
				},
				"securityGroups": map[string]any{
					"groupIds": []any{
						"sg-serviceaccount123",
					},
				},
			},
		},
	}

	// Create the resource
	_, err := s.dynamicClient.Resource(gvr).Namespace(testNamespace).Create(s.ctx, sgp, metav1.CreateOptions{})
	require.NoError(s.T(), err, "Failed to create SecurityGroupPolicy")

	// Retrieve and convert
	retrieved, err := s.dynamicClient.Resource(gvr).Namespace(testNamespace).Get(s.ctx, "integration-test-sgp-sa", metav1.GetOptions{})
	require.NoError(s.T(), err, "Failed to get SecurityGroupPolicy")

	converted, err := ConvertUnstructuredToAWSNetworkPolicy(retrieved)
	require.NoError(s.T(), err, "Failed to convert SecurityGroupPolicy")
	require.NotNil(s.T(), converted)

	sgpData := converted.GetAwsSecurityGroupPolicy()
	require.NotNil(s.T(), sgpData)

	// Verify service account selector
	require.NotNil(s.T(), sgpData.ServiceAccountSelector)
	assert.Equal(s.T(), "external-access", sgpData.ServiceAccountSelector.MatchLabels["role"])

	// Cleanup
	err = s.dynamicClient.Resource(gvr).Namespace(testNamespace).Delete(s.ctx, "integration-test-sgp-sa", metav1.DeleteOptions{})
	assert.NoError(s.T(), err)
}

// Test ClusterNetworkPolicy with namespace-based subject (POC Scenarios 7-8).
func (s *EKSCNIIntegrationSuite) TestClusterNetworkPolicy_NamespaceSubject() {
	gvr := schema.GroupVersionResource{
		Group:    "networking.k8s.aws",
		Version:  "v1alpha1",
		Resource: "clusternetworkpolicies",
	}

	// Create a ClusterNetworkPolicy with namespace subject
	cnp := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "networking.k8s.aws/v1alpha1",
			"kind":       "ClusterNetworkPolicy",
			"metadata": map[string]any{
				"name": "integration-test-ns-subject",
			},
			"spec": map[string]any{
				"priority": int64(300),
				"tier":     "baseline",
				"subject": map[string]any{
					"namespaces": map[string]any{
						"matchLabels": map[string]any{
							"environment": "production",
						},
					},
				},
				"ingress": []any{
					map[string]any{
						"action": "Deny",
						"from": []any{
							map[string]any{
								"namespaces": map[string]any{
									"matchLabels": map[string]any{
										"environment": "development",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Create the resource
	_, err := s.dynamicClient.Resource(gvr).Create(s.ctx, cnp, metav1.CreateOptions{})
	require.NoError(s.T(), err, "Failed to create ClusterNetworkPolicy")

	// Retrieve and convert
	retrieved, err := s.dynamicClient.Resource(gvr).Get(s.ctx, "integration-test-ns-subject", metav1.GetOptions{})
	require.NoError(s.T(), err, "Failed to get ClusterNetworkPolicy")

	converted, err := ConvertUnstructuredToAWSNetworkPolicy(retrieved)
	require.NoError(s.T(), err, "Failed to convert ClusterNetworkPolicy")
	require.NotNil(s.T(), converted)

	cnpData := converted.GetAwsClusterNetworkPolicy()
	require.NotNil(s.T(), cnpData)

	// Verify namespace subject (not pods)
	require.NotNil(s.T(), cnpData.Subject)
	require.NotNil(s.T(), cnpData.Subject.Namespaces, "Namespace selector should be set")
	assert.Equal(s.T(), "production", cnpData.Subject.Namespaces.MatchLabels["environment"])

	// Verify ingress rule with namespace peer
	require.Len(s.T(), cnpData.IngressRules, 1)
	assert.Equal(s.T(), "Deny", cnpData.IngressRules[0].Action)
	require.Len(s.T(), cnpData.IngressRules[0].From, 1)
	require.NotNil(s.T(), cnpData.IngressRules[0].From[0].Namespaces)
	assert.Equal(s.T(), "development", cnpData.IngressRules[0].From[0].Namespaces.MatchLabels["environment"])

	// Cleanup
	err = s.dynamicClient.Resource(gvr).Delete(s.ctx, "integration-test-ns-subject", metav1.DeleteOptions{})
	assert.NoError(s.T(), err)
}

// Test listing multiple ClusterNetworkPolicy resources.
func (s *EKSCNIIntegrationSuite) TestClusterNetworkPolicy_List() {
	gvr := schema.GroupVersionResource{
		Group:    "networking.k8s.aws",
		Version:  "v1alpha1",
		Resource: "clusternetworkpolicies",
	}

	// Create multiple policies
	policies := []string{"list-test-1", "list-test-2", "list-test-3"}
	for i, name := range policies {
		cnp := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "networking.k8s.aws/v1alpha1",
				"kind":       "ClusterNetworkPolicy",
				"metadata": map[string]any{
					"name": name,
					"labels": map[string]any{
						"test-group": "list-test",
					},
				},
				"spec": map[string]any{
					"priority": int64((i + 1) * 100),
					"tier":     "standard",
					"subject": map[string]any{
						"pods": map[string]any{
							"namespaceSelector": map[string]any{},
							"podSelector":       map[string]any{},
						},
					},
				},
			},
		}
		_, err := s.dynamicClient.Resource(gvr).Create(s.ctx, cnp, metav1.CreateOptions{})
		require.NoError(s.T(), err, "Failed to create policy %s", name)
	}

	// List all policies
	list, err := s.dynamicClient.Resource(gvr).List(s.ctx, metav1.ListOptions{
		LabelSelector: "test-group=list-test",
	})
	require.NoError(s.T(), err, "Failed to list ClusterNetworkPolicies")
	assert.Len(s.T(), list.Items, 3, "Should have 3 policies")

	// Convert each and verify
	for _, item := range list.Items {
		converted, err := ConvertUnstructuredToAWSNetworkPolicy(&item)
		require.NoError(s.T(), err, "Failed to convert ClusterNetworkPolicy")
		require.NotNil(s.T(), converted)
		assert.Equal(s.T(), "ClusterNetworkPolicy", converted.Kind)
		assert.True(s.T(), IsAWSNetworkPolicy(converted.Kind))
	}

	// Cleanup
	for _, name := range policies {
		err = s.dynamicClient.Resource(gvr).Delete(s.ctx, name, metav1.DeleteOptions{})
		assert.NoError(s.T(), err)
	}
}

// Test applying sample YAML files from testdata.
func (s *EKSCNIIntegrationSuite) TestApplySampleYAMLFiles() {
	// Apply sample ClusterNetworkPolicy YAML
	cmd := exec.CommandContext(s.ctx, "kubectl", "apply", "-f",
		"testdata/eks-cni/samples/cluster-network-policy.yaml",
		"--context", "kind-"+eksIntegrationClusterName)
	err := cmd.Run()
	require.NoError(s.T(), err, "Failed to apply sample ClusterNetworkPolicy YAML")

	// Apply sample SecurityGroupPolicy YAML
	cmd = exec.CommandContext(s.ctx, "kubectl", "apply", "-f",
		"testdata/eks-cni/samples/security-group-policy.yaml",
		"--context", "kind-"+eksIntegrationClusterName)
	err = cmd.Run()
	require.NoError(s.T(), err, "Failed to apply sample SecurityGroupPolicy YAML")

	// Verify ClusterNetworkPolicies were created
	cnpGVR := schema.GroupVersionResource{
		Group:    "networking.k8s.aws",
		Version:  "v1alpha1",
		Resource: "clusternetworkpolicies",
	}
	cnpList, err := s.dynamicClient.Resource(cnpGVR).List(s.ctx, metav1.ListOptions{})
	require.NoError(s.T(), err)
	assert.GreaterOrEqual(s.T(), len(cnpList.Items), 4, "Should have at least 4 ClusterNetworkPolicies from sample file")

	// Verify SecurityGroupPolicies were created
	sgpGVR := schema.GroupVersionResource{
		Group:    "vpcresources.k8s.aws",
		Version:  "v1beta1",
		Resource: "securitygrouppolicies",
	}
	sgpList, err := s.dynamicClient.Resource(sgpGVR).Namespace(testNamespace).List(s.ctx, metav1.ListOptions{})
	require.NoError(s.T(), err)
	assert.GreaterOrEqual(s.T(), len(sgpList.Items), 2, "Should have at least 2 SecurityGroupPolicies from sample file")

	// Cleanup
	_ = exec.CommandContext(s.ctx, "kubectl", "delete", "-f",
		"testdata/eks-cni/samples/cluster-network-policy.yaml",
		"--context", "kind-"+eksIntegrationClusterName).Run()
	_ = exec.CommandContext(s.ctx, "kubectl", "delete", "-f",
		"testdata/eks-cni/samples/security-group-policy.yaml",
		"--context", "kind-"+eksIntegrationClusterName).Run()
}
