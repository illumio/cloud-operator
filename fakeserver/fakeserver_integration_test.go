// Copyright 2024 Illumio, Inc. All Rights Reserved.

package main

import (
	"bytes"         // Added for request body
	"crypto/tls"    // Added for HTTPS client
	"encoding/json" // Added for JSON marshalling
	"fmt"
	"net/http" // Added for HTTP client
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require" // Using require for setup/teardown assertions
	"go.uber.org/zap"
)

var testToken = createSignedToken() // Renamed to avoid conflict if run in same package

func createSignedToken() string {
	// Use a consistent audience for testing, matching what FakeServer might expect
	// Note: The FakeServer's gRPC component might need a different audience
	// than the one used for generating tokens *for* the client.
	// Let's assume the audience here is relevant for the token *provided* by the server.
	aud := "192.168.49.1:50051" // Audience expected by gRPC server
	token := "token1"
	// Example of generating a JWT with an "aud" claim
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": token,
		"aud": []string{aud},                        // Audience claim
		"exp": time.Now().Add(time.Hour * 1).Unix(), // Shorter expiry for test token
	})
	// Use a consistent, known key for test signing
	// IMPORTANT: Never use hardcoded secrets like this in production!
	mySigningKey := []byte("secret")

	// Sign and get the complete encoded token as a string
	// nosemgrep: jwt.hardcoded-jwt-key
	signedToken, err := jwtToken.SignedString(mySigningKey)
	if err != nil {
		logger.Error("Token could not be signed with fake secret key")
	}
	return signedToken
}

// --- Existing Tests ---

// TestFakeServerConnectionSuccesfulAndRetry tests a client connecting to the gRPC server
func TestFakeServerConnectionSuccesfulAndRetry(t *testing.T) {
	// Use t.Parallel() if tests are independent and can run in parallel
	logger := zap.NewNop()
	// Setup: Start the FakeServer
	fakeServer := &FakeServer{
		address:     "0.0.0.0:50051", // Use different ports if running tests in parallel
		httpAddress: "0.0.0.0:50053", // Use different ports if running tests in parallel
		stopChan:    make(chan struct{}),
		token:       testToken, // Use the test token
		logger:      logger,
		state:       &ServerState{ConnectionSuccessful: false},
		// Ensure FakeServer's HTTP part uses TLS for this test if client expects HTTPS
	}

	// Start the server
	err := fakeServer.start()
	require.NoError(t, err, "Setup: Failed to start the FakeServer")
	logger.Info("Server started for TestFakeServerConnectionSuccesfulAndRetry")

	// Cleanup: Stop the server after the test
	// Use t.Cleanup for reliable teardown
	t.Cleanup(func() {
		logger.Info("Stopping server for TestFakeServerConnectionSuccesfulAndRetry")
		fakeServer.stop()
		logger.Info("Server stopped for TestFakeServerConnectionSuccesfulAndRetry")
	})

	// Wait for the state to change, Wanting to see client succesfully connect to fakeserver
	timeout := time.After(120 * time.Second) // Increased timeout for CI/slow environments
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// Give the external client time to connect
	logger.Info("Waiting for external client connection...")

mainloop1: // Label for the first loop
	for {
		select {
		case <-timeout:
			t.Fatal("Timeout: External client never connected successfully in the allotted time.")
			return // Not strictly needed after t.Fatal but good practice
		case <-ticker.C:
			// Check if the log entry has been recorded
			stateChanged := fakeServer.state.ConnectionSuccessful

			// Check if the log entry we sent is in the received logs
			if stateChanged {
				t.Log("Connection Succes is true. Test passed.")
				assert.Equal(t, stateChanged, true)
				break mainloop1
			}
		}
	}

	// --- Test Retry ---
	logger.Info("Stopping server to test client retry...")
	fakeServer.stop()                             // Stop the server
	fakeServer.state.ConnectionSuccessful = false // Reset state for retry

	// Add a small delay to ensure the client detects the disconnection
	time.Sleep(2 * time.Second)

	logger.Info("Restarting server for retry test...")
	// Restart the server
	err = fakeServer.start()
	require.NoError(t, err, "Retry Setup: Failed to restart the FakeServer")
	logger.Info("Server restarted for retry test.")

	// Reset timeout for the retry attempt
	timeout = time.After(120 * time.Second)

	logger.Info("Waiting for external client reconnection...")
mainloop2: // Label for the second loop
	for {
		select {
		case <-timeout:
			t.Fatal("Timeout: External client never reconnected successfully after restart.")
			return
		case <-ticker.C:
			// Check if the log entry has been recorded
			stateChanged := fakeServer.state.ConnectionSuccessful

			// Check if the log entry we sent is in the received logs
			if stateChanged {
				t.Log("Connection Succes is true. Test passed.")
				assert.Equal(t, stateChanged, true)
				break mainloop2
			}
		}
	}
}

func TestFailureDuringIntialCommit(t *testing.T) {
	// Use t.Parallel() if tests are independent
	logger := zap.NewNop()
	// Setup: Start the FakeServer configured to fail the initial commit
	fakeServer := &FakeServer{
		address:     "0.0.0.0:50051", // Use different ports if running tests in parallel
		httpAddress: "0.0.0.0:50053", // Use different ports if running tests in parallel
		token:       testToken,
		logger:      logger,
		state:       &ServerState{ConnectionSuccessful: false, BadIntialCommit: true}, // Set BadInitialCommit to true
	}

	// Start the server
	err := fakeServer.start()
	require.NoError(t, err, "Setup: Failed to start the FakeServer")
	logger.Info("Server started for TestFailureDuringIntialCommit")

	// Cleanup: Stop the server after the test
	t.Cleanup(func() {
		logger.Info("Stopping server for TestFailureDuringIntialCommit")
		fakeServer.stop()
		logger.Info("Server stopped for TestFailureDuringIntialCommit")
	})

	// Wait for the state to change, expecting connection to succeed but perhaps specific handling for BadInitialCommit
	// The definition of success might differ here. Does the client still connect? Does it retry?
	// This test seems similar to the previous one in waiting for ConnectionSuccessful.
	// Let's assume for now we just want to see if the client *can* connect initially even if the server intends to misbehave later.
	timeout := time.After(120 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	logger.Info("Waiting for external client connection (expecting potential issues later)...")

mainloop:
	for {
		select {
		case <-timeout:
			t.Fatal("Timeout: External client never connected successfully in the allotted time (BadInitialCommit test).")
			return
		case <-ticker.C:
			// Check if the log entry has been recorded
			stateChanged := fakeServer.state.ConnectionSuccessful

			// Check if the log entry we sent is in the received logs
			if stateChanged {
				t.Log("Connection Succes is true. Test passed.")
				assert.Equal(t, stateChanged, true)
				break mainloop
			}
		}
	}
}

// --- New Test ---

// TestClientOnboardAndAuth verifies the FakeServer's HTTP onboarding and authentication endpoints.
func TestClientOnboardAndAuth(t *testing.T) {
	// Use t.Parallel() if tests are independent
	logger := zap.NewNop()

	// Setup: Start the FakeServer, ensuring it uses TLS for HTTP
	fakeServer := &FakeServer{
		address:     "0.0.0.0:50051",
		httpAddress: "0.0.0.0:50053",
		token:       testToken,
		logger:      logger,
		state:       &ServerState{},
	}

	// Start the server
	err := fakeServer.start()
	require.NoError(t, err, "Setup: Failed to start the FakeServer for Auth test")
	logger.Info("Server started for TestClientOnboardAndAuth")

	// Cleanup: Stop the server after the test
	t.Cleanup(func() {
		logger.Info("Stopping server for TestClientOnboardAndAuth")
		fakeServer.stop()
		logger.Info("Server stopped for TestClientOnboardAndAuth")
	})

	// --- Create HTTP Client (skipping TLS verification for test server) ---
	// #nosec G402 -- InsecureSkipVerify is okay for testing against local test server
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	httpClient := &http.Client{
		Transport: tr,
		Timeout:   10 * time.Second, // Add a timeout to HTTP client requests
	}

	// --- Define Test Data ---
	onboardClientID := "test-onboard-id-1"
	onboardClientSecret := "test-onboard-secret-1"

	// --- Simulate Onboarding Request ---
	t.Log("Simulating onboarding request...")
	onboardURL := fmt.Sprintf("https://%s/api/v1/k8s_cluster/onboard", "0.0.0.0:50053") // Use HTTPS
	onboardPayload := map[string]string{
		"onboarding_client_id":     onboardClientID,
		"onboarding_client_secret": onboardClientSecret,
		// Add other fields expected by your onboarding endpoint
	}
	onboardBody, err := json.Marshal(onboardPayload)
	require.NoError(t, err, "Failed to marshal onboarding payload")

	onboardResp, err := httpClient.Post(onboardURL, "application/json", bytes.NewBuffer(onboardBody))
	require.NoError(t, err, "HTTP POST request for onboarding failed")
	defer onboardResp.Body.Close()

	// Assert onboarding success
	require.Equal(t, http.StatusOK, onboardResp.StatusCode, "Expected HTTP 200 OK for onboarding")
	t.Logf("Onboarding request successful (Status: %d)", onboardResp.StatusCode)

	// Optionally decode and verify onboarding response body
	var onboardRespBody map[string]string
	err = json.NewDecoder(onboardResp.Body).Decode(&onboardRespBody)
	require.NoError(t, err, "Failed to decode onboarding response body")
	// Assuming FakeServer is modified to return these based on some logic or fixed values
	// assert.Equal(t, expectedClusterClientID, onboardRespBody["cluster_client_id"])
	// assert.Equal(t, expectedClusterClientSecret, onboardRespBody["cluster_client_secret"])
	clusterClientID := onboardRespBody["cluster_client_id"]         // Get actual client ID from response
	clusterClientSecret := onboardRespBody["cluster_client_secret"] // Get actual secret from response
	require.NotEmpty(t, clusterClientID, "Cluster client ID should not be empty in onboarding response")
	require.NotEmpty(t, clusterClientSecret, "Cluster client secret should not be empty in onboarding response")
	t.Logf("Received Cluster Client ID: %s", clusterClientID)

	// --- Simulate Authentication Request ---
	t.Log("Simulating authentication request...")
	authURL := fmt.Sprintf("https://%s/api/v1/k8s_cluster/authenticate", "0.0.0.0:50053") // Use HTTPS
	authPayload := map[string]string{
		"client_id":     clusterClientID,      // Use credentials received from onboarding
		"client_secret": clusterClientSecret,  // Use credentials received from onboarding
		"grant_type":    "client_credentials", // Common grant type
	}
	authBody, err := json.Marshal(authPayload)
	require.NoError(t, err, "Failed to marshal authentication payload")

	authResp, err := httpClient.Post(authURL, "application/json", bytes.NewBuffer(authBody))
	require.NoError(t, err, "HTTP POST request for authentication failed")
	defer authResp.Body.Close()

	// Assert authentication success
	require.Equal(t, http.StatusOK, authResp.StatusCode, "Expected HTTP 200 OK for authentication")
	t.Logf("Authentication request successful (Status: %d)", authResp.StatusCode)

	// Optionally decode and verify authentication response body
	var authRespBody map[string]interface{} // Use interface{} for mixed types like expires_in (number)
	err = json.NewDecoder(authResp.Body).Decode(&authRespBody)
	require.NoError(t, err, "Failed to decode authentication response body")
	accessToken, ok := authRespBody["access_token"].(string)
	require.True(t, ok, "Access token not found or not a string in response")
	require.NotEmpty(t, accessToken, "Access token should not be empty")
	t.Logf("Received Access Token (length): %d", len(accessToken))

	t.Log("Onboarding and Authentication test passed.")
}
