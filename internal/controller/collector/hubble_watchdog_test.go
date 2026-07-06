// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"errors"
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

// serverStatus builds a ServerStatusResponse with the given connected and
// unavailable node counts.
func serverStatus(connected, unavailable uint32, unavailableNodes ...string) *observer.ServerStatusResponse {
	return &observer.ServerStatusResponse{
		NumConnectedNodes:   wrapperspb.UInt32(connected),
		NumUnavailableNodes: wrapperspb.UInt32(unavailable),
		UnavailableNodes:    unavailableNodes,
	}
}

// TestMonitorRelayPeerHealth_WarnsOnUnavailableNodes verifies that the watchdog
// logs a Warn naming the unavailable peers when Relay reports some agents it
// cannot reach.
func TestMonitorRelayPeerHealth_WarnsOnUnavailableNodes(t *testing.T) {
	core, logs := zapobserver.New(zap.WarnLevel)
	logger := zap.New(core)

	mockClient := &mockObserverClient{}
	mockClient.On("ServerStatus", mock.Anything, mock.Anything, mock.Anything).
		Return(serverStatus(6, 2, "node-a", "node-b"), nil)

	fm := &CiliumFlowCollector{
		logger:                   logger,
		client:                   mockClient,
		relayStatusCheckInterval: 5 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go fm.monitorRelayPeerHealth(ctx)

	require.Eventually(t, func() bool {
		return logs.FilterMessageSnippet("cannot reach some agent nodes").Len() > 0
	}, time.Second, 5*time.Millisecond, "expected a warning about unavailable nodes")

	entry := logs.FilterMessageSnippet("cannot reach some agent nodes").All()[0]
	assert.Contains(t, entry.ContextMap()["unavailable_node_names"], "node-a")
	assert.EqualValues(t, 6, entry.ContextMap()["connected_nodes"])
}

// TestMonitorRelayPeerHealth_WarnsOnZeroConnectedNodes verifies that the
// watchdog logs a distinct Warn when Relay reports zero connected nodes, which
// is the silent flows_received: 0 case.
func TestMonitorRelayPeerHealth_WarnsOnZeroConnectedNodes(t *testing.T) {
	core, logs := zapobserver.New(zap.WarnLevel)
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

	go fm.monitorRelayPeerHealth(ctx)

	require.Eventually(t, func() bool {
		return logs.FilterMessageSnippet("zero connected agent nodes").Len() > 0
	}, time.Second, 5*time.Millisecond, "expected a warning about zero connected nodes")
}

// TestMonitorRelayPeerHealth_HealthyLogsInfo verifies that a fully healthy peer
// status logs at Info level and does not emit a warning.
func TestMonitorRelayPeerHealth_HealthyLogsInfo(t *testing.T) {
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

	go fm.monitorRelayPeerHealth(ctx)

	require.Eventually(t, func() bool {
		return logs.FilterMessageSnippet("peer status healthy").Len() > 0
	}, time.Second, 5*time.Millisecond, "expected a healthy status info log")

	assert.Zero(t, logs.FilterLevelExact(zap.WarnLevel).Len(), "healthy status must not warn")
}

// TestMonitorRelayPeerHealth_StatusRPCErrorSkipsTick verifies that a failing
// ServerStatus RPC is logged at Debug and does not produce a peer-health
// warning or info line.
func TestMonitorRelayPeerHealth_StatusRPCErrorSkipsTick(t *testing.T) {
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

	go fm.monitorRelayPeerHealth(ctx)

	// Give the watchdog several poll intervals.
	time.Sleep(40 * time.Millisecond)

	assert.Zero(t, logs.Len(), "status RPC errors must not emit info/warn peer-health logs")
}

// TestMonitorRelayPeerHealth_StopsOnContextDone verifies the watchdog returns
// promptly when its context is cancelled.
func TestMonitorRelayPeerHealth_StopsOnContextDone(t *testing.T) {
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
		fm.monitorRelayPeerHealth(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watchdog did not return after context cancellation")
	}
}
