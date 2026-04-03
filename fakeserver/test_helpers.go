// Copyright 2024 Illumio, Inc. All Rights Reserved.

package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// TestConfig holds configuration for integration tests.
type TestConfig struct {
	GRPCAddress   string
	HTTPAddress   string
	Timeout       time.Duration
	PollInterval  time.Duration
	EnableLogging bool
}

// DefaultTestConfig returns sensible defaults for testing.
func DefaultTestConfig() TestConfig {
	return TestConfig{
		GRPCAddress:   "0.0.0.0:50051",
		HTTPAddress:   "0.0.0.0:50053",
		Timeout:       90 * time.Second, // Reduced from 120s
		PollInterval:  500 * time.Millisecond,
		EnableLogging: true, // Enable logging by default for debugging
	}
}

// CreateTestToken creates a signed JWT token for testing.
// Uses a fixed expiration time to ensure all tests generate identical tokens,
// allowing the operator to reconnect across test restarts.
func CreateTestToken(audience string) string {
	// Use a fixed expiration time (year 2099) so token is deterministic
	fixedExpiration := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "token1",
		"aud": []string{audience},
		"exp": fixedExpiration,
	})

	mySigningKey := []byte("secret")

	// nosemgrep: jwt.hardcoded-jwt-key
	signedToken, err := jwtToken.SignedString(mySigningKey)
	if err != nil {
		panic(fmt.Sprintf("Failed to sign token: %v", err))
	}

	return signedToken
}

// CreateTestLogger creates a logger for tests.
func CreateTestLogger(t *testing.T, enabled bool) *zap.Logger {
	t.Helper()

	if !enabled {
		return zap.NewNop()
	}

	config := zap.NewDevelopmentConfig()
	config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	config.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("15:04:05.000")

	logger, err := config.Build()
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	return logger.Named("fakeserver-test")
}

// FakeServerTestHarness wraps FakeServer with test utilities.
type FakeServerTestHarness struct {
	Server        *FakeServer
	EnhancedState *EnhancedServerState
	Config        TestConfig
	T             *testing.T
}

// NewTestHarness creates a new test harness.
func NewTestHarness(t *testing.T, config TestConfig) *FakeServerTestHarness {
	t.Helper()

	logger := CreateTestLogger(t, config.EnableLogging)
	token := CreateTestToken("192.168.49.1:50051")
	enhancedState := NewEnhancedServerState()

	server := &FakeServer{
		address:     config.GRPCAddress,
		httpAddress: config.HTTPAddress,
		token:       token,
		logger:      logger,
		state:       &ServerState{}, // Legacy state
	}

	return &FakeServerTestHarness{
		Server:        server,
		EnhancedState: enhancedState,
		Config:        config,
		T:             t,
	}
}

// Start starts the fake server.
func (h *FakeServerTestHarness) Start() error {
	h.T.Log("Starting FakeServer...")

	err := h.Server.start()
	if err != nil {
		return fmt.Errorf("failed to start FakeServer: %w", err)
	}

	h.T.Logf("FakeServer started on gRPC=%s, HTTP=%s", h.Config.GRPCAddress, h.Config.HTTPAddress)

	return nil
}

// Stop stops the fake server.
func (h *FakeServerTestHarness) Stop() {
	h.T.Log("Stopping FakeServer...")
	h.Server.stop()
	h.T.Log("FakeServer stopped")
}

// WaitForCondition waits for a condition to become true.
func (h *FakeServerTestHarness) WaitForCondition(condition func() bool, description string) error {
	h.T.Logf("Waiting for: %s (timeout: %v)", description, h.Config.Timeout)

	timeout := time.After(h.Config.Timeout)

	ticker := time.NewTicker(h.Config.PollInterval)
	defer ticker.Stop()

	startTime := time.Now()
	checkCount := 0

	for {
		select {
		case <-timeout:
			elapsed := time.Since(startTime)
			h.T.Logf("TIMEOUT after %v (%d checks): %s", elapsed, checkCount, description)
			h.LogCurrentState()

			return fmt.Errorf("timeout waiting for: %s", description)

		case <-ticker.C:
			checkCount++

			if condition() {
				elapsed := time.Since(startTime)
				h.T.Logf("SUCCESS after %v (%d checks): %s", elapsed, checkCount, description)

				return nil
			}

			// Log progress every 10 seconds
			if checkCount%20 == 0 {
				elapsed := time.Since(startTime)
				h.T.Logf("Still waiting (%v elapsed): %s", elapsed, description)
				h.LogCurrentState()
			}
		}
	}
}

// WaitForConnection waits for the operator to connect and complete resource snapshot.
func (h *FakeServerTestHarness) WaitForConnection() error {
	return h.WaitForCondition(
		func() bool { return h.Server.state.ConnectionSuccessful },
		"operator connection successful (resource snapshot complete)",
	)
}

// LogCurrentState logs the current server state for debugging.
func (h *FakeServerTestHarness) LogCurrentState() {
	state := h.Server.state
	h.T.Logf("Current state: ConnectionSuccessful=%v, BadInitialCommit=%v",
		state.ConnectionSuccessful, state.BadIntialCommit)
}

// ResetState resets the server state for a new test phase.
func (h *FakeServerTestHarness) ResetState() {
	h.Server.state.ConnectionSuccessful = false
	h.Server.state.BadIntialCommit = false
	h.T.Log("Server state reset")
}

// SetBadInitialCommit configures the server to fail the initial commit.
func (h *FakeServerTestHarness) SetBadInitialCommit(bad bool) {
	h.Server.state.BadIntialCommit = bad
	h.T.Logf("BadInitialCommit set to: %v", bad)
}
