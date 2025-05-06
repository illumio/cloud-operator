// Copyright 2024 Illumio, Inc. All Rights Reserved.

package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// createSignedToken creates a JWT token for testing.
// It now returns an error if signing fails.
// The audience matches the FakeServer's gRPC address used in tests.
func createSignedTokenForTest(logger *zap.Logger) (string, error) {
	// This audience should match the address the gRPC server is listening on
	// and what the client might be targeting, or what the server expects in its validation.
	// For tests where FakeServer listens on "0.0.0.0:50051", this is a reasonable audience.
	aud := "0.0.0.0:50051"
	tokenSubject := "test-token-subject" // Changed from "token1" for clarity

	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": tokenSubject,
		"aud": []string{aud},
		"exp": time.Now().Add(time.Hour * 1).Unix(), // Shorter expiry for tests
	})

	// Use the same signing key as configured in FakeServer if it were to validate the signature.
	// For the current FakeServer, it only checks the string value of the token.
	mySigningKey := []byte("your-very-secret-and-strong-key-for-hs256") // Match main.go if needed

	signedToken, err := jwtToken.SignedString(mySigningKey)
	if err != nil {
		if logger != nil { // Only log if a logger is provided
			logger.Error("Token could not be signed with fake secret key", zap.Error(err))
		}
		return "", fmt.Errorf("failed to sign token: %w", err)
	}
	return signedToken, nil
}

// TestFakeServerConnectionSuccessfulAndRetry tests a client connecting to the gRPC server
func TestFakeServerConnectionSuccessfulAndRetry(t *testing.T) {
	testLogger := zap.NewNop() // No-op logger for tests

	signedToken, err := createSignedTokenForTest(testLogger)
	assert.NoError(t, err, "Failed to create signed token for test")

	// Initial server setup
	fakeServer := &FakeServer{
		address:     "0.0.0.0:50051",
		httpAddress: "0.0.0.0:50053",
		// stopChan is removed as per FakeServer refactor
		token:  signedToken,
		logger: testLogger,
		state:  &ServerState{ConnectionSuccessful: false},
		// authService is initialized internally by FakeServer.Start()
	}

	// --- First Connection Attempt ---
	t.Log("Starting FakeServer for the first connection attempt...")
	err = fakeServer.Start()
	assert.NoError(t, err, "Failed to start the FakeServer (1st attempt)")

	// Wait for the state to change, indicating client successfully connected
	timeoutDuration := 60 * time.Second // Reduced timeout for faster test failure
	operationTimeout := time.After(timeoutDuration)
	ticker := time.NewTicker(500 * time.Millisecond)
	connectionSuccessful := false

	t.Log("Waiting for successful connection (1st attempt)...")
mainloop1:
	for {
		select {
		case <-operationTimeout:
			t.Fatal("Timeout: Operator never connected successfully (1st attempt)")
			ticker.Stop() // Stop ticker before returning
			return
		case <-ticker.C:
			if fakeServer.state.ConnectionSuccessful {
				t.Log("ConnectionSuccessful is true (1st attempt). Test segment passed.")
				connectionSuccessful = true
				ticker.Stop() // Stop current ticker
				break mainloop1
			}
		}
	}
	assert.True(t, connectionSuccessful, "Expected ConnectionSuccessful to be true (1st attempt)")

	// Stop the server after the first attempt
	t.Log("Stopping FakeServer after 1st attempt...")
	stopCtx1, cancelStop1 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStop1()
	err = fakeServer.Stop(stopCtx1)
	assert.NoError(t, err, "Failed to stop the FakeServer (1st attempt)")
	t.Log("FakeServer stopped (1st attempt).")

	// --- Second Connection Attempt (Retry Logic) ---
	// Reset state for the retry
	fakeServer.state.ConnectionSuccessful = false
	// Note: FakeServer would need to be re-initialized or have its internal state reset
	// if Start() doesn't fully re-initialize listeners and servers.
	// The current FakeServer.Start() re-listens and creates new gRPC/HTTP servers,
	// so re-calling Start() on the same instance should be okay.
	// However, for cleaner tests, creating a new FakeServer instance might be more robust.
	// For now, we'll reuse and restart.

	t.Log("Restarting FakeServer for the second connection attempt (retry)...")
	err = fakeServer.Start() // Restart the server
	assert.NoError(t, err, "Failed to restart the FakeServer (2nd attempt)")
	defer func() { // Ensure server is stopped at the end of this test function
		t.Log("Ensuring FakeServer is stopped at the end of the test (defer)...")
		stopCtxDefer, cancelDefer := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelDefer()
		if stopErr := fakeServer.Stop(stopCtxDefer); stopErr != nil {
			t.Logf("Error stopping FakeServer in defer: %v", stopErr)
		}
	}()

	operationTimeout2 := time.After(timeoutDuration) // New timeout for the second attempt
	ticker2 := time.NewTicker(500 * time.Millisecond)
	connectionSuccessfulRetry := false

	t.Log("Waiting for successful connection (2nd attempt/retry)...")
mainloop2:
	for {
		select {
		case <-operationTimeout2:
			t.Fatal("Timeout: Operator never connected successfully (2nd attempt/retry)")
			ticker2.Stop()
			return
		case <-ticker2.C:
			if fakeServer.state.ConnectionSuccessful {
				t.Log("ConnectionSuccessful is true (2nd attempt/retry). Test segment passed.")
				connectionSuccessfulRetry = true
				ticker2.Stop()
				break mainloop2
			}
		}
	}
	assert.True(t, connectionSuccessfulRetry, "Expected ConnectionSuccessful to be true (2nd attempt/retry)")
	t.Log("TestFakeServerConnectionSuccessfulAndRetry completed.")
}

func TestFailureDuringInitialCommit(t *testing.T) {
	testLogger := zap.NewNop()

	signedToken, err := createSignedTokenForTest(testLogger)
	assert.NoError(t, err, "Failed to create signed token for test")

	// Setup: Start the FakeServer configured to simulate a bad initial commit
	fakeServer := &FakeServer{
		address:     "0.0.0.0:50051",
		httpAddress: "0.0.0.0:50053",
		token:       signedToken,
		logger:      testLogger,
		state:       &ServerState{ConnectionSuccessful: false, BadInitialCommit: true},
		// authService is initialized internally by FakeServer.Start()
	}

	t.Log("Starting FakeServer for TestFailureDuringInitialCommit...")
	err = fakeServer.Start()
	assert.NoError(t, err, "Failed to start the FakeServer")

	// Cleanup: Stop the server after the test
	defer func() {
		t.Log("Stopping FakeServer for TestFailureDuringInitialCommit (defer)...")
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if stopErr := fakeServer.Stop(stopCtx); stopErr != nil {
			t.Errorf("Error stopping FakeServer in defer: %v", stopErr)
		}
	}()

	// In this test, we expect the connection to be made,
	// an initial commit to happen, and the server to simulate a failure (BadInitialCommit = true).
	// The gRPC stream `SendKubernetesResources` in `fakeserver.go` returns an error or EOF
	// when `BadInitialCommit` is true and `ResourceSnapshotComplete` is received.
	// The client (operator) should ideally handle this.
	// This test primarily checks if the server starts and can be stopped.
	// To verify the "failure" aspect, you'd typically need a mock client that
	// asserts it received an error or that the stream closed prematurely.

	// For this test, we'll check that ConnectionSuccessful *does not* become true
	// if BadInitialCommit causes an early stream termination before success is marked,
	// OR if ConnectionSuccessful *does* become true but then the stream errors out.
	// The current FakeServer logic sets ConnectionSuccessful = true *then* checks BadInitialCommit.
	// So, ConnectionSuccessful might become true briefly.
	// A more robust test would involve a client.

	// Let's wait for a short period to see if ConnectionSuccessful becomes true,
	// even though BadInitialCommit is set.
	timeoutDuration := 30 * time.Second // Shorter duration, as we are testing a failure scenario
	operationTimeout := time.After(timeoutDuration)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	initialCommitProcessed := false

	t.Log("Monitoring server state for initial commit processing...")
mainloop:
	for {
		select {
		case <-operationTimeout:
			t.Log("Timeout reached. Assuming server processed requests as expected for BadInitialCommit scenario.")
			// If BadInitialCommit is true, ConnectionSuccessful might still flip to true
			// if the snapshot complete message is processed before the simulated error.
			// The key is that the client *should* see an issue.
			// This test, without a client, can only verify server behavior.
			if fakeServer.state.ConnectionSuccessful {
				t.Log("ConnectionSuccessful became true, which is possible before BadInitialCommit error is returned.")
			} else {
				t.Log("ConnectionSuccessful remained false, as expected if error occurs before it's set or if no connection.")
			}
			// We can also check if BadInitialCommit was reset (meaning it was triggered)
			assert.False(t, fakeServer.state.BadInitialCommit, "Expected BadInitialCommit to be reset by the server logic")
			break mainloop
		case <-ticker.C:
			// The gRPC handler sets ConnectionSuccessful = true *then* checks BadInitialCommit.
			// So, ConnectionSuccessful might become true.
			if fakeServer.state.ConnectionSuccessful {
				initialCommitProcessed = true // Indicates the point of success was reached
			}
			// If BadInitialCommit was true and then reset, it means the logic was hit.
			if initialCommitProcessed && !fakeServer.state.BadInitialCommit {
				t.Log("BadInitialCommit logic was triggered and reset. ConnectionSuccessful might have been true briefly.")
				assert.True(t, initialCommitProcessed, "Expected initial commit to be processed")
				assert.False(t, fakeServer.state.BadInitialCommit, "Expected BadInitialCommit to be reset")
				break mainloop
			}
		}
	}
	t.Log("TestFailureDuringInitialCommit completed.")
}
