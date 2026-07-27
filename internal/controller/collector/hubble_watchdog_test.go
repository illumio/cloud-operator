// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	observer "github.com/cilium/cilium/api/v1/observer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// loggedNodeNames normalizes a zap.Strings log field read back via ContextMap,
// which reflects slices to []interface{}, into a []string.
func loggedNodeNames(t *testing.T, v any) []string {
	t.Helper()

	switch nodes := v.(type) {
	case []string:
		return nodes
	case []any:
		out := make([]string, 0, len(nodes))
		for _, n := range nodes {
			s, ok := n.(string)
			require.True(t, ok, "node name should be a string")

			out = append(out, s)
		}

		return out
	default:
		t.Fatalf("unexpected type for unavailableNodes: %T", v)

		return nil
	}
}

// serverStatus builds a ServerStatusResponse with the given connected and
// unavailable node counts.
func serverStatus(connected, unavailable uint32, unavailableNodes ...string) *observer.ServerStatusResponse {
	return &observer.ServerStatusResponse{
		NumConnectedNodes:   wrapperspb.UInt32(connected),
		NumUnavailableNodes: wrapperspb.UInt32(unavailable),
		UnavailableNodes:    unavailableNodes,
	}
}

// TestMonitorHubbleRelayServerStatus_WarnsOnUnavailableNodes verifies that the
// watchdog logs both the unconditional Info status line and a Warn (naming the
// unavailable nodes) when Relay reports agents it cannot reach.
func TestMonitorHubbleRelayServerStatus_WarnsOnUnavailableNodes(t *testing.T) {
	core, logs := zapobserver.New(zap.InfoLevel)
	logger := zap.New(core)

	mockClient := &mockObserverClient{}
	mockClient.On("ServerStatus", mock.Anything, mock.Anything, mock.Anything).
		Return(serverStatus(6, 2, "node-b", "node-a"), nil)

	fm := &CiliumFlowCollector{
		logger:                   logger,
		client:                   mockClient,
		relayStatusCheckInterval: 5 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go fm.monitorHubbleRelayServerStatus(ctx)

	require.Eventually(t, func() bool {
		return logs.FilterMessageSnippet("cannot reach").Len() > 0
	}, time.Second, 5*time.Millisecond, "expected a warning about unavailable nodes")

	// The unconditional Info status line is always present.
	statusEntry := logs.FilterMessage("Hubble Relay server status").All()[0]
	assert.EqualValues(t, 6, statusEntry.ContextMap()["numConnectedNodes"])
	assert.EqualValues(t, 2, statusEntry.ContextMap()["numUnavailableNodes"])

	// The Warn line names the unavailable nodes, sorted.
	warnEntry := logs.FilterMessageSnippet("cannot reach").All()[0]
	assert.Equal(t, zap.WarnLevel, warnEntry.Level)
	assert.Equal(t, []string{"node-a", "node-b"}, loggedNodeNames(t, warnEntry.ContextMap()["unavailableNodes"]))
}

// TestMonitorHubbleRelayServerStatus_WarnsOnZeroConnectedNodes verifies that the
// watchdog warns when Relay reports zero connected nodes, which is the silent
// zero-flow case, even when no nodes are explicitly listed as unavailable.
func TestMonitorHubbleRelayServerStatus_WarnsOnZeroConnectedNodes(t *testing.T) {
	core, logs := zapobserver.New(zap.InfoLevel)
	logger := zap.New(core)

	mockClient := &mockObserverClient{}
	mockClient.On("ServerStatus", mock.Anything, mock.Anything, mock.Anything).
		Return(serverStatus(0, 0), nil)

	fm := &CiliumFlowCollector{
		logger:                   logger,
		client:                   mockClient,
		relayStatusCheckInterval: 5 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go fm.monitorHubbleRelayServerStatus(ctx)

	require.Eventually(t, func() bool {
		return logs.FilterLevelExact(zap.WarnLevel).Len() > 0
	}, time.Second, 5*time.Millisecond, "expected a warning about zero connected nodes")
}

// TestMonitorHubbleRelayServerStatus_HealthyLogsInfoOnly verifies that a fully
// healthy status logs the single Info status line and does not warn.
func TestMonitorHubbleRelayServerStatus_HealthyLogsInfoOnly(t *testing.T) {
	core, logs := zapobserver.New(zap.InfoLevel)
	logger := zap.New(core)

	mockClient := &mockObserverClient{}
	mockClient.On("ServerStatus", mock.Anything, mock.Anything, mock.Anything).
		Return(serverStatus(8, 0), nil)

	fm := &CiliumFlowCollector{
		logger:                   logger,
		client:                   mockClient,
		relayStatusCheckInterval: 5 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go fm.monitorHubbleRelayServerStatus(ctx)

	require.Eventually(t, func() bool {
		return logs.FilterMessage("Hubble Relay server status").Len() > 0
	}, time.Second, 5*time.Millisecond, "expected the info status log")

	assert.Zero(t, logs.FilterLevelExact(zap.WarnLevel).Len(), "healthy status must not warn")
}

// TestMonitorHubbleRelayServerStatus_TruncatesUnavailableNodes verifies that a
// large unavailable-node list is sorted and truncated in the log entry.
func TestMonitorHubbleRelayServerStatus_TruncatesUnavailableNodes(t *testing.T) {
	core, logs := zapobserver.New(zap.WarnLevel)
	logger := zap.New(core)

	// Build more nodes than the log cap, in unsorted order.
	total := maxLoggedUnavailableNodes + 5

	nodes := make([]string, 0, total)
	for i := total; i > 0; i-- {
		nodes = append(nodes, fmt.Sprintf("node-%03d", i))
	}

	mockClient := &mockObserverClient{}
	mockClient.On("ServerStatus", mock.Anything, mock.Anything, mock.Anything).
		Return(serverStatus(1, uint32(total), nodes...), nil)

	fm := &CiliumFlowCollector{
		logger:                   logger,
		client:                   mockClient,
		relayStatusCheckInterval: 5 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go fm.monitorHubbleRelayServerStatus(ctx)

	require.Eventually(t, func() bool {
		return logs.FilterMessageSnippet("cannot reach").Len() > 0
	}, time.Second, 5*time.Millisecond, "expected a warning about unavailable nodes")

	warnEntry := logs.FilterMessageSnippet("cannot reach").All()[0]
	logged := loggedNodeNames(t, warnEntry.ContextMap()["unavailableNodes"])
	assert.Len(t, logged, maxLoggedUnavailableNodes, "list should be truncated")
	assert.Equal(t, "node-001", logged[0], "list should be sorted ascending")
}

// TestMonitorHubbleRelayServerStatus_RPCErrorWarnsAndSkipsTick verifies that a
// failing ServerStatus RPC is logged at Warn and does not produce a status line.
func TestMonitorHubbleRelayServerStatus_RPCErrorWarnsAndSkipsTick(t *testing.T) {
	core, logs := zapobserver.New(zap.InfoLevel)
	logger := zap.New(core)

	mockClient := &mockObserverClient{}
	mockClient.On("ServerStatus", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("relay unreachable"))

	fm := &CiliumFlowCollector{
		logger:                   logger,
		client:                   mockClient,
		relayStatusCheckInterval: 5 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go fm.monitorHubbleRelayServerStatus(ctx)

	require.Eventually(t, func() bool {
		return logs.FilterMessageSnippet("Failed to query Hubble Relay ServerStatus").Len() > 0
	}, time.Second, 5*time.Millisecond, "expected a warning about the failed RPC")

	assert.Zero(t, logs.FilterMessage("Hubble Relay server status").Len(),
		"a failed status RPC must not emit a status line")
}

// TestMonitorHubbleRelayServerStatus_StopsOnContextDone verifies the watchdog
// returns promptly when its context is cancelled.
func TestMonitorHubbleRelayServerStatus_StopsOnContextDone(t *testing.T) {
	logger := zap.NewNop()

	mockClient := &mockObserverClient{}
	mockClient.On("ServerStatus", mock.Anything, mock.Anything, mock.Anything).
		Return(serverStatus(8, 0), nil)

	fm := &CiliumFlowCollector{
		logger:                   logger,
		client:                   mockClient,
		relayStatusCheckInterval: 5 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})

	go func() {
		fm.monitorHubbleRelayServerStatus(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watchdog did not return after context cancellation")
	}
}
