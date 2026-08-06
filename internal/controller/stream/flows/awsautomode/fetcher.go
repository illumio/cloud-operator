// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsautomode

import (
	"context"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"

	"github.com/illumio/cloud-operator/internal/controller/collector"
)

// restLogFetcher fetches a node's Network Policy Agent log through the kubelet
// node-proxy endpoint using the shared clientset's REST client. It builds:
//
//	GET /api/v1/nodes/{node}/proxy/logs/aws-routed-eni/network-policy-agent.log
//
// There is no typed client-go helper for nodes/proxy log files, so this uses the
// raw REST request builder. It reuses the operator's in-cluster credentials,
// rate limiter, and transport — no node IP, host mount, or AWS SDK involved.
type restLogFetcher struct {
	k8sClient kubernetes.Interface
	logPath   string
	// logger is optional (nil-safe); when set, the constructed node-proxy request
	// URL is logged at Debug before the request is issued.
	logger *zap.Logger
}

// fetch retrieves the full log file bytes for a node.
func (f *restLogFetcher) fetch(ctx context.Context, nodeName string) ([]byte, error) {
	segments := collector.NetworkPolicyAgentLogPathSegments(f.logPath)

	req := f.k8sClient.CoreV1().RESTClient().
		Get().
		Resource("nodes").
		Name(nodeName).
		SubResource("proxy").
		Suffix(segments...)

	if f.logger != nil {
		f.logger.Debug("EKS Auto Mode fetching node-proxy log",
			zap.String("node", nodeName),
			zap.String("url", req.URL().String()))
	}

	return req.Do(ctx).Raw()
}
