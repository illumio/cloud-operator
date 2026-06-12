// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsvpccni

import (
	"bufio"
	"bytes"
	"context"
	"io"
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
	c.logger.Info("Starting AWS VPC CNI flow collector",
		zap.Duration("poll_interval", c.pollInterval))

	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()

	// Do an initial poll immediately
	if err := c.pollAllPods(ctx); err != nil {
		c.logger.Warn("AWS VPC CNI initial poll failed", zap.Error(err))
	}

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("AWS VPC CNI flow collector stopping")

			return ctx.Err()
		case <-ticker.C:
			if err := c.pollAllPods(ctx); err != nil {
				c.logger.Warn("AWS VPC CNI poll cycle failed", zap.Error(err))
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

	c.logger.Debug("AWS VPC CNI polling aws-node pods", zap.Int("count", len(pods.Items)))

	// Get logs from each running pod
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}

		podLogger := c.logger.With(zap.String("pod", pod.Name))
		if err := c.pollPod(ctx, pod, podLogger); err != nil {
			podLogger.Debug("AWS VPC CNI failed to poll pod",
				zap.Error(err))
		}
	}

	// Cleanup stale entries from lastPollTime
	c.cleanupStalePods(pods)

	return nil
}

// pollPod gets logs from a single aws-node pod since the last poll.
func (c *vpccniClient) pollPod(ctx context.Context, pod *corev1.Pod, logger *zap.Logger) error {
	c.mu.Lock()
	sinceTime := c.lastPollTime[pod.Name]
	c.mu.Unlock()

	// If first poll for this pod, get logs from the last poll interval
	if sinceTime.IsZero() {
		sinceTime = time.Now().Add(-c.pollInterval)
	}

	// Capture poll start time before fetching logs to avoid gaps
	// between request start and when we store lastPollTime
	pollStart := time.Now()

	logs, err := c.getLogsFromPod(ctx, pod.Name, sinceTime)
	if err != nil {
		return err
	}

	// Update last poll time to poll start to avoid missing logs
	c.mu.Lock()
	c.lastPollTime[pod.Name] = pollStart
	c.mu.Unlock()

	// Parse and cache flows
	c.parseLogs(ctx, logs, logger)

	return nil
}

// getLogsFromPod retrieves logs from a pod's aws-eks-nodeagent container.
func (c *vpccniClient) getLogsFromPod(ctx context.Context, podName string, since time.Time) ([]byte, error) {
	sinceTime := metav1.NewTime(since)
	req := c.k8sClient.CoreV1().Pods(collector.AWSNodeNamespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: collector.AWSEksNodeagentContainer,
		SinceTime: &sinceTime,
	})

	stream, err := req.Stream(ctx)
	if err != nil {
		return nil, err
	}
	defer stream.Close() //nolint:errcheck

	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, stream); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// parseLogs parses log lines and caches flows.
func (c *vpccniClient) parseLogs(ctx context.Context, logs []byte, logger *zap.Logger) {
	if len(logs) == 0 {
		return
	}

	scanner := bufio.NewScanner(bytes.NewReader(logs))
	flowCount := 0

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		flow, err := collector.ParseAWSVPCCNIFlowLog(string(line))
		if err != nil {
			continue
		}

		if err := c.flowSink.CacheFlow(ctx, flow); err != nil {
			logger.Debug("AWS VPC CNI failed to cache flow",
				zap.Error(err))

			continue
		}

		c.flowSink.IncrementFlowsReceived()

		flowCount++
	}

	if flowCount > 0 {
		logger.Debug("AWS VPC CNI parsed flows from pod",
			zap.Int("flow_count", flowCount))
	}
}

// cleanupStalePods removes entries from lastPollTime for pods that no longer exist.
func (c *vpccniClient) cleanupStalePods(pods *corev1.PodList) {
	activePods := make(map[string]bool, len(pods.Items))
	for _, pod := range pods.Items {
		activePods[pod.Name] = true
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for podName := range c.lastPollTime {
		if !activePods[podName] {
			delete(c.lastPollTime, podName)
			c.logger.Debug("AWS VPC CNI removed stale pod from tracking", zap.String("pod", podName))
		}
	}
}
