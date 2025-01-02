package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// Simulating a client interacting with the gRPC server
func TestFakeServerStateChange(t *testing.T) {
	// Setup: Start the FakeServer
	fakeServer := &FakeServer{
		address:     "localhost:50051",
		httpAddress: "localhost:8080",
		token:       "test-token",
		logger:      zap.NewNop(), // Use a no-op logger for testing
		state:       &ServerState{ConnectionSuccessful: false},
	}

	// Start the server
	err := fakeServer.start()
	assert.NoError(t, err, "Failed to start the FakeServer")

	// Cleanup: Stop the server after the test
	defer fakeServer.stop()

	// Allow the server a bit of time to start
	time.Sleep(2 * time.Second)

	// NOTE TO SELF: TEST IS FAILING HERE NOT SURE WHY!
	// Wait for the state to change, Wanting to see client succesfully connect to fakeserver
	timeout := time.After(15 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			// Test failure if the state hasn't changed in time
			t.Fatal("State did not change in time")
		case <-ticker.C:
			t.Log(fakeServer.state.ConnectionSuccessful)
			// Check if the log entry has been recorded
			stateChanged := fakeServer.state.ConnectionSuccessful

			// Check if the log entry we sent is in the received logs
			if stateChanged {
				t.Log("Connection Succes is true. Test passed.")
				return
			}

		}
	}
}
