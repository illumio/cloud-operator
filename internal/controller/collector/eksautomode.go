// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"strings"

	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// EKSComputeTypeLabel is the node label EKS applies to Auto Mode nodes.
	EKSComputeTypeLabel = "eks.amazonaws.com/compute-type"
	// EKSComputeTypeAuto is the value of EKSComputeTypeLabel on Auto Mode nodes.
	EKSComputeTypeAuto = "auto"
)

// DefaultNetworkPolicyAgentLogPath is the node-local path (relative to the
// kubelet log root) of the AWS-managed Network Policy Agent log. It is the
// default for the configurable log path; AWS controls this component, so the
// path is overridable in case a future agent version relocates the file.
const DefaultNetworkPolicyAgentLogPath = "aws-routed-eni/network-policy-agent.log"

// NetworkPolicyAgentLogPathSegments returns the kubelet-proxy log path segments
// (prefixed with "logs") used to fetch the given node-local log path via the
// node proxy. An empty logPath falls back to DefaultNetworkPolicyAgentLogPath.
func NetworkPolicyAgentLogPathSegments(logPath string) []string {
	if logPath == "" {
		logPath = DefaultNetworkPolicyAgentLogPath
	}

	segments := []string{"logs"}

	for part := range strings.SplitSeq(logPath, "/") {
		if part != "" {
			segments = append(segments, part)
		}
	}

	return segments
}

// IsEKSAutoModeAvailable reports whether the cluster has at least one EKS Auto
// Mode node. In Auto Mode the VPC CNI and Network Policy Agent are AWS-managed
// (no aws-node DaemonSet is present), so IsAWSVPCCNIAvailable returns false and
// standard pod-log collection cannot be used. Auto Mode nodes are identified by
// the node label eks.amazonaws.com/compute-type=auto.
//
// This is a positive signal (the presence of the Auto Mode label) rather than
// inferring Auto Mode from the absence of aws-node, so a cluster with neither
// aws-node nor the Auto Mode label is not mistaken for Auto Mode.
func IsEKSAutoModeAvailable(ctx context.Context, logger *zap.Logger, k8sClient kubernetes.Interface) bool {
	nodes, err := k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: EKSComputeTypeLabel + "=" + EKSComputeTypeAuto,
		Limit:         1,
	})
	if err != nil {
		logger.Debug("Failed to list nodes for EKS Auto Mode detection", zap.Error(err))

		return false
	}

	if len(nodes.Items) == 0 {
		logger.Debug("No EKS Auto Mode nodes found")

		return false
	}

	logger.Debug("EKS Auto Mode node detected",
		zap.String("node", nodes.Items[0].Name))

	return true
}
