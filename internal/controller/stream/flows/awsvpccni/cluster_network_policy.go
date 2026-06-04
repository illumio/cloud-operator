// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsvpccni

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const (
	// ClusterNetworkPolicyAPIVersion is the API version for AWS VPC CNI ClusterNetworkPolicy.
	ClusterNetworkPolicyAPIVersion = "networking.k8s.aws/v1alpha1"
	// ClusterNetworkPolicyName is the name of the policy created by the operator.
	ClusterNetworkPolicyName = "illumio-cloud-operator-flow-logging"
)

// clusterNetworkPolicyGVR is the GroupVersionResource for AWS VPC CNI ClusterNetworkPolicy.
var clusterNetworkPolicyGVR = schema.GroupVersionResource{
	Group:    "networking.k8s.aws",
	Version:  "v1alpha1",
	Resource: "clusternetworkpolicies",
}

// IsCRDAvailable checks if the ClusterNetworkPolicy CRD is registered in the cluster.
func IsCRDAvailable(logger *zap.Logger, discoveryClient discovery.DiscoveryInterface) bool {
	_, err := discoveryClient.ServerResourcesForGroupVersion(ClusterNetworkPolicyAPIVersion)
	if err != nil {
		logger.Warn("AWS VPC CNI ClusterNetworkPolicy CRD not available", zap.Error(err))

		return false
	}

	logger.Debug("AWS VPC CNI ClusterNetworkPolicy CRD is available")

	return true
}

// EnsureFlowLoggingPolicy creates the ClusterNetworkPolicy for comprehensive flow logging.
// If the policy already exists, it returns nil (idempotent).
// Sets an owner reference to the cloud-operator Deployment so the policy is cleaned up on uninstall.
func EnsureFlowLoggingPolicy(ctx context.Context, logger *zap.Logger, dynamicClient dynamic.Interface, k8sClient kubernetes.Interface, podNamespace string) error {
	ownerRefs, err := getDeploymentOwnerReference(ctx, k8sClient, podNamespace)
	if err != nil {
		logger.Warn("AWS VPC CNI could not resolve owner Deployment for ClusterNetworkPolicy, policy will not be auto-deleted on uninstall",
			zap.Error(err))
	}

	metadata := map[string]any{
		"name": ClusterNetworkPolicyName,
		"labels": map[string]any{
			"app.kubernetes.io/managed-by": "illumio-cloud-operator",
			"app.kubernetes.io/component":  "flow-logging",
		},
	}

	if len(ownerRefs) > 0 {
		metadata["ownerReferences"] = ownerRefs
	}

	policy := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": ClusterNetworkPolicyAPIVersion,
			"kind":       "ClusterNetworkPolicy",
			"metadata":   metadata,
			"spec": map[string]any{
				"tier":     "Baseline",
				"priority": int64(1000),
				"subject": map[string]any{
					"namespaces": map[string]any{
						"matchLabels": map[string]any{},
					},
				},
				"ingress": []any{
					map[string]any{
						"action": "Pass",
						"from": []any{
							map[string]any{
								"namespaces": map[string]any{
									"matchLabels": map[string]any{},
								},
							},
						},
					},
				},
				"egress": []any{
					map[string]any{
						"action": "Pass",
						"to": []any{
							map[string]any{
								"namespaces": map[string]any{
									"matchLabels": map[string]any{},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err = dynamicClient.Resource(clusterNetworkPolicyGVR).Create(ctx, policy, metav1.CreateOptions{})
	if err != nil {
		// Policy is cluster-scoped and persists across restarts of the cloud-operator pod.
		if apierrors.IsAlreadyExists(err) {
			logger.Debug("AWS VPC CNI ClusterNetworkPolicy already exists", zap.String("name", ClusterNetworkPolicyName))

			return nil
		}

		logger.Error("AWS VPC CNI failed to create ClusterNetworkPolicy", zap.Error(err))

		return err
	}

	logger.Info("AWS VPC CNI created ClusterNetworkPolicy for comprehensive flow logging", zap.String("name", ClusterNetworkPolicyName))

	return nil
}

// getDeploymentOwnerReference looks up the cloud-operator Deployment and returns an ownerReferences
// list suitable for unstructured objects so the ClusterNetworkPolicy is cleaned up on uninstall.
func getDeploymentOwnerReference(ctx context.Context, k8sClient kubernetes.Interface, namespace string) ([]any, error) {
	deployments, err := k8sClient.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=cloud-operator",
	})
	if err != nil {
		return nil, fmt.Errorf("listing cloud-operator deployments: %w", err)
	}

	if len(deployments.Items) == 0 {
		return nil, fmt.Errorf("no cloud-operator deployment found in namespace %s", namespace)
	}

	deploy := &deployments.Items[0]

	return []any{
		map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"name":       deploy.Name,
			"uid":        string(deploy.UID),
		},
	}, nil
}
