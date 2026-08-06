// Copyright 2026 Illumio, Inc. All Rights Reserved.

// Package awsautomode collects AWS VPC CNI Network Policy Agent flow logs on EKS
// Auto Mode clusters.
//
// On EKS Auto Mode the VPC CNI and its Network Policy Agent are AWS-managed and
// are NOT exposed as pods, so the standard aws-node pod-log collector cannot see
// them. Instead the agent writes a node-local log file
// (/var/log/aws-routed-eni/network-policy-agent.log) which this collector reads
// through the Kubernetes kubelet node-proxy endpoint:
//
//	GET /api/v1/nodes/{node}/proxy/logs/aws-routed-eni/network-policy-agent.log
//
// This endpoint does not stream, so the collector polls each node on an interval,
// using a per-node checkpoint to fetch only new records and to handle log
// rotation, truncation, and node restarts.
//
// The node-proxy log endpoint is polled rather than streamed. It uses only the
// operator service account, in-cluster API server access, and Kubernetes RBAC
// (nodes + nodes/proxy). It never connects to node IPs directly, mounts host
// paths, runs privileged, or uses AWS credentials/SDKs.
package awsautomode

import (
	"bufio"
	"bytes"
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/illumio/cloud-operator/internal/controller/collector"
)

// nodeLogFetcher fetches the full Network Policy Agent log for a node through the
// kubelet node-proxy endpoint. Abstracted so tests can back it with httptest.
type nodeLogFetcher interface {
	fetch(ctx context.Context, nodeName string) ([]byte, error)
}

// autoModeClient implements the flow collector for EKS Auto Mode nodes.
type autoModeClient struct {
	logger              *zap.Logger
	flowSink            collector.FlowSink
	k8sClient           kubernetes.Interface
	fetcher             nodeLogFetcher
	pollInterval        time.Duration
	maxConcurrentPolls  int
	checkpoints         *checkpointStore
	statsAutoModeNodes  func(int)
	statsAutoModeErrors func()
}

// Run polls every EKS Auto Mode node's Network Policy Agent log on an interval
// until the context is cancelled.
func (c *autoModeClient) Run(ctx context.Context) error {
	c.logger.Info("Starting EKS Auto Mode flow collector (node-proxy log polling)",
		zap.Duration("poll_interval", c.pollInterval),
		zap.Int("max_concurrent_node_polls", c.maxConcurrentPolls))

	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()

	// Initial poll immediately so flows start without waiting a full interval.
	c.pollAllNodes(ctx)

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("EKS Auto Mode flow collector stopping")

			return ctx.Err()
		case <-ticker.C:
			c.pollAllNodes(ctx)
		}
	}
}

// pollAllNodes lists Auto Mode nodes and polls each with bounded concurrency.
func (c *autoModeClient) pollAllNodes(ctx context.Context) {
	nodes, err := c.k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: collector.EKSComputeTypeLabel + "=" + collector.EKSComputeTypeAuto,
	})
	if err != nil {
		c.logger.Warn("EKS Auto Mode failed to list nodes", zap.Error(err))

		if c.statsAutoModeErrors != nil {
			c.statsAutoModeErrors()
		}

		return
	}

	if c.statsAutoModeNodes != nil {
		c.statsAutoModeNodes(len(nodes.Items))
	}

	c.logger.Debug("EKS Auto Mode polling nodes", zap.Int("count", len(nodes.Items)))

	sem := make(chan struct{}, c.maxConcurrentPolls)

	var wg sync.WaitGroup

	activeNodes := make(map[string]bool, len(nodes.Items))
	for i := range nodes.Items {
		node := &nodes.Items[i]
		activeNodes[node.Name] = true

		wg.Go(func() {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}

			defer func() { <-sem }()

			nodeLogger := c.logger.With(zap.String("node", node.Name))

			if err := c.pollNode(ctx, node.Name, node.UID, nodeLogger); err != nil {
				nodeLogger.Debug("EKS Auto Mode failed to poll node", zap.Error(err))

				if c.statsAutoModeErrors != nil {
					c.statsAutoModeErrors()
				}
			}
		})
	}

	wg.Wait()

	// Drop checkpoints for nodes that no longer exist.
	for _, removed := range c.checkpoints.retain(activeNodes) {
		c.logger.Debug("EKS Auto Mode removed stale node checkpoint", zap.String("node", removed))
	}
}

// pollNode fetches a single node's log, reconciles it against the checkpoint,
// processes only new records, and persists the advanced checkpoint ONLY after
// the records are successfully processed.
func (c *autoModeClient) pollNode(ctx context.Context, nodeName string, nodeUID types.UID, logger *zap.Logger) error {
	cp := c.checkpoints.get(nodeName)

	data, err := c.fetcher.fetch(ctx, nodeName)
	if err != nil {
		return err
	}

	slice := reconcile(cp, data, nodeUID)
	if slice.reset {
		logger.Debug("EKS Auto Mode detected log reset (rotation/truncation/node replacement); reprocessing from start")
	}

	// Process the new complete records. The offset is only advanced (persisted)
	// after processing, so a mid-cycle failure never skips unprocessed records.
	c.processLines(ctx, slice.lines, logger)

	next := slice.next
	c.checkpoints.set(nodeName, &next)

	return nil
}

// processLines parses each log line and caches any flows found.
func (c *autoModeClient) processLines(ctx context.Context, lines [][]byte, logger *zap.Logger) {
	flowCount := 0

	for _, line := range lines {
		// Defensively split on any embedded newlines (should not happen).
		scanner := bufio.NewScanner(bytes.NewReader(line))
		for scanner.Scan() {
			b := scanner.Bytes()
			if len(b) == 0 {
				continue
			}

			flow, err := collector.ParseAWSVPCCNIFlowLog(string(b))
			if err != nil {
				continue
			}

			if err := c.flowSink.CacheFlow(ctx, flow); err != nil {
				logger.Debug("EKS Auto Mode failed to cache flow", zap.Error(err))

				continue
			}

			c.flowSink.IncrementFlowsReceived()

			flowCount++
		}
	}

	if flowCount > 0 {
		logger.Debug("EKS Auto Mode parsed flows from node", zap.Int("flow_count", flowCount))
	}
}
