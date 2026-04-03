// Copyright 2024 Illumio, Inc. All Rights Reserved.

package main

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestOperatorSendsCiliumFlows tests that the operator sends Cilium flows
// when Cilium with Hubble is available in the cluster.
// This test requires Cilium with Hubble Relay to be installed.
// Set CILIUM_ENABLED=true to run this test.
func TestOperatorSendsCiliumFlows(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if os.Getenv("CILIUM_ENABLED") != "true" {
		t.Skip("Skipping Cilium flow test: CILIUM_ENABLED not set")
	}

	config := DefaultTestConfig()
	// Allow more time for flows to arrive
	config.Timeout = 3 * time.Minute
	harness := NewTestHarness(t, config)

	// Start the server
	err := harness.Start()
	require.NoError(t, err, "Failed to start FakeServer")

	defer harness.Stop()

	// Wait for operator to connect first
	err = harness.WaitForConnection()
	require.NoError(t, err, "Operator failed to connect")

	t.Log("Operator connected, waiting for Cilium flows...")

	// Wait for at least one Cilium flow to be received
	err = harness.WaitForCondition(
		func() bool { return harness.Server.state.CiliumFlowsReceived > 0 },
		"Cilium flows received",
	)
	require.NoError(t, err, "No Cilium flows received")

	t.Logf("Test passed: Received %d Cilium flows", harness.Server.state.CiliumFlowsReceived)
}
