// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"fmt"
	"strconv"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	goldmanepb "github.com/illumio/cloud-operator/api/illumio/cloud/goldmane/v1"
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/goldmane"
)

// CalicoFlowCollector collects flows from Calico Goldmane running in this cluster.
type CalicoFlowCollector struct {
	logger *zap.Logger
	client goldmanepb.FlowsClient
}

// newCalicoFlowCollector connects to Calico Goldmane, sets up a Flows client,
// and returns a new Collector using it.
func newCalicoFlowCollector(ctx context.Context, logger *zap.Logger, calicoNamespace string) (*CalicoFlowCollector, error) {
	clientset, err := NewClientSet()
	if err != nil {
		return nil, fmt.Errorf("failed to create new client set: %w", err)
	}

	// Step 1: Discover Goldmane service
	service, err := goldmane.DiscoverGoldmane(ctx, calicoNamespace, clientset, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to discover Goldmane: %w", err)
	}

	goldmaneAddress := goldmane.GetAddressFromService(service)

	// Step 2: Get TLS config from secret
	tlsConfig, err := goldmane.GetTLSConfig(ctx, clientset, logger, calicoNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get Goldmane TLS config: %w", err)
	}

	// Step 3: Connect to Goldmane
	conn, err := goldmane.ConnectToGoldmane(logger, goldmaneAddress, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Goldmane: %w", err)
	}

	flowsClient := goldmanepb.NewFlowsClient(conn)

	return &CalicoFlowCollector{logger: logger, client: flowsClient}, nil
}

// exportCalicoFlows streams flows from Goldmane and sends them to the flow cache.
func (c *CalicoFlowCollector) exportCalicoFlows(ctx context.Context, sm *streamManager) error {
	req := &goldmanepb.StreamRequest{}

	stream, err := c.client.Stream(ctx, req)
	if err != nil {
		c.logger.Error("Error starting Goldmane flow stream", zap.Error(err))

		return err
	}

	for {
		select {
		case <-ctx.Done():
			c.logger.Warn("Context cancelled, stopping Calico flow export")

			return ctx.Err()
		default:
		}

		resp, err := stream.Recv()
		if err != nil {
			c.logger.Warn("Failed to receive flow from Goldmane stream", zap.Error(err))

			return err
		}

		calicoFlow := convertGoldmaneFlow(resp)
		if calicoFlow == nil {
			continue
		}

		err = sm.FlowCache.CacheFlow(ctx, calicoFlow)
		if err != nil {
			c.logger.Error("Failed to cache Calico flow", zap.Error(err))

			return err
		}
	}
}

// convertGoldmaneFlow converts a Goldmane StreamResponse to a CalicoFlow.
func convertGoldmaneFlow(resp *goldmanepb.StreamResponse) *pb.CalicoFlow {
	if resp == nil || resp.GetFlow() == nil || resp.GetFlow().GetKey() == nil {
		return nil
	}

	flow := resp.GetFlow()
	key := flow.GetKey()

	// Parse timestamps (Goldmane returns Unix timestamps as strings)
	startTime := parseUnixTimestamp(flow.GetStartTime())
	endTime := parseUnixTimestamp(flow.GetEndTime())

	// Parse destination port
	destPort := parseUint32(key.GetDestPort())
	destServicePort := parseUint32(key.GetDestServicePort())

	// Parse statistics
	packetsIn := parseUint64(flow.GetPacketsIn())
	packetsOut := parseUint64(flow.GetPacketsOut())
	bytesIn := parseUint64(flow.GetBytesIn())
	bytesOut := parseUint64(flow.GetBytesOut())
	numConnectionsStarted := parseUint64(flow.GetNumConnectionsStarted())
	numConnectionsCompleted := parseUint64(flow.GetNumConnectionsCompleted())
	numConnectionsLive := parseUint64(flow.GetNumConnectionsLive())

	// Convert policies
	var policies *pb.CalicoPolicies
	if key.GetPolicies() != nil {
		policies = convertGoldmanePolicies(key.GetPolicies())
	}

	calicoFlow := &pb.CalicoFlow{
		StartTime:               startTime,
		EndTime:                 endTime,
		SourceName:              key.GetSourceName(),
		SourceNamespace:         key.GetSourceNamespace(),
		SourceType:              key.GetSourceType(),
		DestName:                key.GetDestName(),
		DestNamespace:           key.GetDestNamespace(),
		DestType:                key.GetDestType(),
		DestPort:                destPort,
		DestServiceName:         key.GetDestServiceName(),
		DestServiceNamespace:    key.GetDestServiceNamespace(),
		DestServicePortName:     key.GetDestServicePortName(),
		DestServicePort:         destServicePort,
		Proto:                   key.GetProto(),
		Reporter:                key.GetReporter(),
		Action:                  key.GetAction(),
		SourceLabels:            flow.GetSourceLabels(),
		DestLabels:              flow.GetDestLabels(),
		PacketsIn:               packetsIn,
		PacketsOut:              packetsOut,
		BytesIn:                 bytesIn,
		BytesOut:                bytesOut,
		NumConnectionsStarted:   numConnectionsStarted,
		NumConnectionsCompleted: numConnectionsCompleted,
		NumConnectionsLive:      numConnectionsLive,
		Policies:                policies,
	}

	return calicoFlow
}

// convertGoldmanePolicies converts Goldmane Policies to CalicoPolices.
func convertGoldmanePolicies(policies *goldmanepb.Policies) *pb.CalicoPolicies {
	if policies == nil {
		return nil
	}

	enforcedPolicies := make([]*pb.CalicoPolicy, 0, len(policies.GetEnforcedPolicies()))
	for _, p := range policies.GetEnforcedPolicies() {
		enforcedPolicies = append(enforcedPolicies, &pb.CalicoPolicy{
			Kind:      p.GetKind(),
			Namespace: p.GetNamespace(),
			Name:      p.GetName(),
			Tier:      p.GetTier(),
			Action:    p.GetAction(),
		})
	}

	pendingPolicies := make([]*pb.CalicoPolicy, 0, len(policies.GetPendingPolicies()))
	for _, p := range policies.GetPendingPolicies() {
		pendingPolicies = append(pendingPolicies, &pb.CalicoPolicy{
			Kind:      p.GetKind(),
			Namespace: p.GetNamespace(),
			Name:      p.GetName(),
			Tier:      p.GetTier(),
			Action:    p.GetAction(),
		})
	}

	return &pb.CalicoPolicies{
		EnforcedPolicies: enforcedPolicies,
		PendingPolicies:  pendingPolicies,
	}
}

// parseUnixTimestamp parses a Unix timestamp string to a protobuf Timestamp.
func parseUnixTimestamp(s string) *timestamppb.Timestamp {
	if s == "" {
		return nil
	}

	seconds, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}

	return &timestamppb.Timestamp{Seconds: seconds}
}

// parseUint32 parses a string to uint32.
func parseUint32(s string) uint32 {
	if s == "" {
		return 0
	}

	val, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0
	}

	return uint32(val)
}

// parseUint64 parses a string to uint64.
func parseUint64(s string) uint64 {
	if s == "" {
		return 0
	}

	val, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}

	return val
}
