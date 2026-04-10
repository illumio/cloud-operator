// Copyright 2026 Illumio, Inc. All Rights Reserved.

package vpccni

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/illumio/cloud-operator/internal/controller/collector"
)

// vpccniClient implements FlowCollector for VPC CNI flow collection.
type vpccniClient struct {
	logger       *zap.Logger
	flowSink     collector.FlowSink
	k8sClient    kubernetes.Interface
	pollInterval time.Duration

	mu           sync.Mutex
	lastPollTime map[string]time.Time
}

// Run collects flows from VPC CNI by polling aws-node pod logs.
func (c *vpccniClient) Run(ctx context.Context) error {
	c.logger.Info("Starting VPC CNI flow collector",
		zap.Duration("pollInterval", c.pollInterval))

	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()

	// Do an initial poll immediately
	if err := c.pollAllPods(ctx); err != nil {
		c.logger.Warn("Initial poll failed", zap.Error(err))
	}

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("VPC CNI flow collector stopping")

			return ctx.Err()
		case <-ticker.C:
			if err := c.pollAllPods(ctx); err != nil {
				c.logger.Warn("Poll cycle failed", zap.Error(err))
			}
		}
	}
}

// pollAllPods lists all aws-node pods and gets logs from each.
func (c *vpccniClient) pollAllPods(ctx context.Context) error {
	// List aws-node pods
	pods, err := c.k8sClient.CoreV1().Pods(collector.AWSNodeNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: collector.AWSNodeLabel,
	})
	if err != nil {
		return err
	}

	c.logger.Debug("Polling aws-node pods", zap.Int("count", len(pods.Items)))

	// Get logs from each running pod
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}

		if err := c.pollPod(ctx, pod); err != nil {
			c.logger.Debug("Failed to poll pod",
				zap.String("pod", pod.Name),
				zap.Error(err))
		}
	}

	// Cleanup stale entries from lastPollTime
	c.cleanupStalePods(pods)

	return nil
}

// pollPod gets logs from a single aws-node pod since the last poll.
func (c *vpccniClient) pollPod(ctx context.Context, pod *corev1.Pod) error {
	c.mu.Lock()
	sinceTime := c.lastPollTime[pod.Name]
	c.mu.Unlock()

	// If first poll for this pod, get logs from the last poll interval
	if sinceTime.IsZero() {
		sinceTime = time.Now().Add(-c.pollInterval)
	}

	logs, err := c.getLogsFromPod(ctx, pod.Name, sinceTime)
	if err != nil {
		return err
	}

	// Update last poll time
	c.mu.Lock()
	c.lastPollTime[pod.Name] = time.Now()
	c.mu.Unlock()

	// Parse and cache flows
	c.parseLogs(ctx, logs, pod.Name)

	return nil
}

// getLogsFromPod retrieves logs from a pod's aws-eks-nodeagent container.
func (c *vpccniClient) getLogsFromPod(ctx context.Context, podName string, since time.Time) (string, error) {
	sinceTime := metav1.NewTime(since)
	req := c.k8sClient.CoreV1().Pods(collector.AWSNodeNamespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: collector.AWSEksNodeagentContainer,
		SinceTime: &sinceTime,
	})

	stream, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer stream.Close()

	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, stream); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// parseLogs parses log lines and caches flows.
func (c *vpccniClient) parseLogs(ctx context.Context, logs string, podName string) {
	if logs == "" {
		return
	}

	lines := strings.Split(logs, "\n")
	flowCount := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		flow, err := collector.ParseVPCCNIFlowLog(line)
		if err != nil {
			// Not a flow log line, skip silently
			continue
		}

		if err := c.flowSink.CacheFlow(ctx, flow); err != nil {
			c.logger.Debug("Failed to cache flow",
				zap.String("pod", podName),
				zap.Error(err))

			continue
		}

		c.flowSink.IncrementFlowsReceived()

		flowCount++
	}

	if flowCount > 0 {
		c.logger.Debug("Parsed flows from pod",
			zap.String("pod", podName),
			zap.Int("flowCount", flowCount))
	}
}

// cleanupStalePods removes entries from lastPollTime for pods that no longer exist.
func (c *vpccniClient) cleanupStalePods(pods *corev1.PodList) {
	activePods := make(map[string]bool)
	for _, pod := range pods.Items {
		activePods[pod.Name] = true
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for podName := range c.lastPollTime {
		if !activePods[podName] {
			delete(c.lastPollTime, podName)
			c.logger.Debug("Removed stale pod from tracking", zap.String("pod", podName))
		}
	}
}
