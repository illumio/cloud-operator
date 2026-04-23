// Copyright 2024 Illumio, Inc. All Rights Reserved.

package vpccni

import (
	"context"

	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
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
		logger.Debug("ClusterNetworkPolicy CRD not available", zap.Error(err))

		return false
	}

	logger.Debug("ClusterNetworkPolicy CRD is available")

	return true
}

// EnsureFlowLoggingPolicy creates the ClusterNetworkPolicy for comprehensive flow logging.
// If the policy already exists, it returns nil (idempotent).
func EnsureFlowLoggingPolicy(ctx context.Context, logger *zap.Logger, dynamicClient dynamic.Interface) error {
	policy := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": ClusterNetworkPolicyAPIVersion,
			"kind":       "ClusterNetworkPolicy",
			"metadata": map[string]any{
				"name": ClusterNetworkPolicyName,
				"labels": map[string]any{
					"app.kubernetes.io/managed-by": "illumio-cloud-operator",
					"app.kubernetes.io/component":  "flow-logging",
				},
			},
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

	_, err := dynamicClient.Resource(clusterNetworkPolicyGVR).Create(ctx, policy, metav1.CreateOptions{})
	if err != nil {
		// Policy is cluster-scoped and persists across operator restarts.
		// Handle AlreadyExists gracefully for idempotent behavior.
		if apierrors.IsAlreadyExists(err) {
			logger.Debug("ClusterNetworkPolicy already exists", zap.String("name", ClusterNetworkPolicyName))

			return nil
		}

		logger.Error("Failed to create ClusterNetworkPolicy", zap.Error(err))

		return err
	}

	logger.Info("Created ClusterNetworkPolicy for comprehensive flow logging", zap.String("name", ClusterNetworkPolicyName))

	return nil
}
