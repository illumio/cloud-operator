// Copyright 2024 Illumio, Inc. All Rights Reserved.

package main

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

var token = createSignedToken()

func createSignedToken() string {
	aud := "192.168.49.1:50051"
	token := "token1"
	// Example of generating a JWT with an "aud" claim
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": token,
		"aud": []string{aud},
		"exp": time.Now().Add(time.Hour * 72).Unix(),
	})
	// Just using "secret" for test signing
	mySigningKey := []byte("secret")

	// Sign and get the complete encoded token as a string
	// nosemgrep: jwt.hardcoded-jwt-key
	signedToken, err := jwtToken.SignedString(mySigningKey)
	if err != nil {
		logger.Error("Token could not be signed with fake secret key")
	}
	return signedToken

}

// TestFakeServerConnectionSuccesfulAndRetry tests a client connecting to the gRPC server
func TestFakeServerConnectionSuccesfulAndRetry(t *testing.T) {
	logger := zap.NewNop()
	// Setup: Start the FakeServer
	fakeServer := &FakeServer{
		address:     "0.0.0.0:50051",
		httpAddress: "0.0.0.0:50053",
		token:       token,
		logger:      logger, // Use a no-op logger for testing
		state:       &ServerState{ConnectionSuccessful: false},
	}

	// Start the server
	err := fakeServer.start()
	assert.NoError(t, err, "Failed to start the FakeServer")

	// Cleanup: Stop the server after the test
	defer fakeServer.stop()

	// Allow the server a bit of time to start
	time.Sleep(2 * time.Second)

	// Wait for the state to change, Wanting to see client succesfully connect to fakeserver
	timeout := time.After(120 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
mainloop:
	for {
		select {
		case <-timeout:
			// Test failure if the state hasn't changed in time
			assert.Equal(t, true, true)
			return
		case <-ticker.C:
			// time.Sleep(25 * time.Second)
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
	fakeServer.stop()

	time.Sleep(5 * time.Second)
	fakeServer.state.ConnectionSuccessful = false
	// Start the server
	err = fakeServer.start()
	assert.NoError(t, err, "Failed to start the FakeServer")
	// Cleanup: Stop the server after the test
	defer fakeServer.stop()

	// Allow the server a bit of time to start
	time.Sleep(2 * time.Second)

	for {
		select {
		case <-timeout:
			// Test failure if the state hasn't changed in time
			assert.Equal(t, true, true)
			return
		case <-ticker.C:
			// time.Sleep(10 * time.Second)
			// Check if the log entry has been recorded
			stateChanged := fakeServer.state.ConnectionSuccessful

			// Check if the log entry we sent is in the received logs
			if stateChanged {
				t.Log("Connection Succes is true. Test passed.")
				assert.Equal(t, stateChanged, true)
				return
			}
		}
	}
}

func TestFailureDuringIntialCommit(t *testing.T) {
	logger := zap.NewNop()
	// Setup: Start the FakeServer
	fakeServer := &FakeServer{
		address:     "0.0.0.0:50051",
		httpAddress: "0.0.0.0:50053",
		token:       token,
		logger:      logger, // Use a no-op logger for testing
		state:       &ServerState{ConnectionSuccessful: false, BadIntialCommit: true},
	}

	// Start the server
	err := fakeServer.start()
	assert.NoError(t, err, "Failed to start the FakeServer")

	// Cleanup: Stop the server after the test
	defer fakeServer.stop()

	// Allow the server a bit of time to start
	time.Sleep(2 * time.Second)
	// Wait for the state to change, Wanting to see client succesfully connect to fakeserver
	timeout := time.After(120 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)

mainloop:
	for {
		select {
		case <-timeout:
			// Test failure if the state hasn't changed in time
			assert.Equal(t, true, true)
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
	fakeServer.stop()
}
