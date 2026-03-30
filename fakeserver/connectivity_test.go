// Copyright 2024 Illumio, Inc. All Rights Reserved.

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOperatorConnectsSuccessfully tests that the operator can connect to the fakeserver
// and complete the initial resource snapshot.
func TestOperatorConnectsSuccessfully(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := DefaultTestConfig()
	harness := NewTestHarness(t, config)

	// Start the server
	err := harness.Start()
	require.NoError(t, err, "Failed to start FakeServer")
	defer harness.Stop()

	// Wait for operator to connect
	err = harness.WaitForConnection()
	require.NoError(t, err, "Operator failed to connect")

	t.Log("Test passed: Operator connected successfully")
}

// TestOperatorReconnectsAfterServerRestart tests that the operator reconnects
// after the server is restarted.
func TestOperatorReconnectsAfterServerRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := DefaultTestConfig()
	harness := NewTestHarness(t, config)

	// Phase 1: Initial connection
	t.Log("=== Phase 1: Initial connection ===")
	err := harness.Start()
	require.NoError(t, err, "Failed to start FakeServer")

	err = harness.WaitForConnection()
	require.NoError(t, err, "Operator failed to connect initially")
	t.Log("Phase 1 passed: Initial connection successful")

	// Phase 2: Restart server and verify reconnection
	t.Log("=== Phase 2: Server restart ===")
	harness.Stop()
	harness.ResetState()

	err = harness.Start()
	require.NoError(t, err, "Failed to restart FakeServer")
	defer harness.Stop()

	err = harness.WaitForConnection()
	require.NoError(t, err, "Operator failed to reconnect after server restart")
	t.Log("Phase 2 passed: Reconnection successful")

	t.Log("Test passed: Operator reconnects after server restart")
}

// TestOperatorHandlesInitialCommitFailure tests that the operator handles
// a failure during the initial commit and retries successfully.
func TestOperatorHandlesInitialCommitFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := DefaultTestConfig()
	harness := NewTestHarness(t, config)

	// Configure server to fail initial commit
	harness.SetBadInitialCommit(true)

	// Start the server
	err := harness.Start()
	require.NoError(t, err, "Failed to start FakeServer")
	defer harness.Stop()

	// Wait for operator to eventually connect (after retry)
	// The server will fail the first attempt, then succeed
	err = harness.WaitForConnection()
	require.NoError(t, err, "Operator failed to connect after initial commit failure")

	t.Log("Test passed: Operator handles initial commit failure")
}
