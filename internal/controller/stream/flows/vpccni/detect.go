// Copyright 2026 Illumio, Inc. All Rights Reserved.

package vpccni

import (
	"context"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// awsNodeLabel is the label selector for aws-node pods.
	awsNodeLabel = "k8s-app=aws-node"
	// awsNodeNamespace is the namespace where aws-node pods run.
	awsNodeNamespace = "kube-system"
	// awsEksNodeagentContainer is the container name that has flow logs.
	awsEksNodeagentContainer = "aws-eks-nodeagent"
)

// IsVPCCNIAvailable checks if AWS VPC CNI with flow logging is available in the cluster.
// It looks for aws-node pods with the aws-eks-nodeagent container.
func IsVPCCNIAvailable(ctx context.Context, logger *zap.Logger, k8sClient kubernetes.Interface) bool {
	pods, err := k8sClient.CoreV1().Pods(awsNodeNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: awsNodeLabel,
		Limit:         1,
	})
	if err != nil {
		logger.Debug("Failed to list aws-node pods", zap.Error(err))

		return false
	}

	if len(pods.Items) == 0 {
		logger.Debug("No aws-node pods found")

		return false
	}

	// Check if the aws-eks-nodeagent container exists
	if hasNodeagentContainer(pods.Items[0]) {
		logger.Debug("VPC CNI with aws-eks-nodeagent detected")

		return true
	}

	logger.Debug("aws-node pods found but aws-eks-nodeagent container not present")

	return false
}

// hasNodeagentContainer checks if the pod has the aws-eks-nodeagent container.
func hasNodeagentContainer(pod corev1.Pod) bool {
	for _, container := range pod.Spec.Containers {
		if container.Name == awsEksNodeagentContainer {
			return true
		}
	}

	return false
}
