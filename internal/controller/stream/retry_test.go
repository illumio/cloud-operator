// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestClamp(t *testing.T) {
	tests := []struct {
		name     string
		minimum  int
		value    int
		maximum  int
		expected int
	}{
		{"value within range", 5, 10, 20, 10},
		{"value below minimum", 5, 2, 20, 5},
		{"value above maximum", 5, 25, 20, 20},
		{"minimum equals maximum", 10, 15, 10, 10},
		{"value equals minimum", 5, 5, 20, 5},
		{"value equals maximum", 5, 20, 20, 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := clamp(tt.minimum, tt.value, tt.maximum)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMarshalLogObject(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	opts := backoffOpts{
		InitialBackoff:       1 * time.Second,
		MaxBackoff:           10 * time.Second,
		MaxJitterPct:         0.1,
		SevereErrorThreshold: 5,
		ExponentialFactor:    2.0,
		Logger:               logger,
	}

	enc := zapcore.NewMapObjectEncoder()
	err = opts.MarshalLogObject(enc)
	require.NoError(t, err)

	assert.Equal(t, opts.InitialBackoff, enc.Fields["initial_backoff"])
	assert.Equal(t, opts.MaxBackoff, enc.Fields["max_backoff"])
	assert.InEpsilon(t, opts.ExponentialFactor, enc.Fields["exponential_factor"], 0.01)
	assert.InEpsilon(t, opts.MaxJitterPct, enc.Fields["max_jitter_pct"], 0.01)
	assert.Equal(t, opts.SevereErrorThreshold, enc.Fields["severe_error_threshold"])
}

func TestExponentialBackoff_SucceedsOnFirstAttempt(t *testing.T) {
	attempts := 0
	action := func() error {
		attempts++
		if attempts == 1 {
			return nil
		}

		return assert.AnError
	}

	opts := backoffOpts{
		InitialBackoff:       1 * time.Millisecond,
		MaxBackoff:           10 * time.Millisecond,
		MaxJitterPct:         0.0,
		SevereErrorThreshold: 1,
		ExponentialFactor:    2.0,
		Logger:               zap.NewNop(),
	}

	done := make(chan error, 1)

	go func() {
		done <- exponentialBackoff(opts, action)
	}()

	select {
	case <-time.After(100 * time.Millisecond):
		t.Fatal("exponentialBackoff did not terminate in time")
	case <-done:
		assert.GreaterOrEqual(t, attempts, 1, "Should have attempted at least once")
	}
}

func TestExponentialBackoff_RetriesOnFailure(t *testing.T) {
	attempts := 0
	action := func() error {
		attempts++

		if attempts < 3 {
			return assert.AnError
		}

		if attempts == 3 {
			return nil
		}

		return assert.AnError
	}

	opts := backoffOpts{
		InitialBackoff:       1 * time.Millisecond,
		MaxBackoff:           10 * time.Millisecond,
		MaxJitterPct:         0.0,
		SevereErrorThreshold: 3,
		ExponentialFactor:    2.0,
		Logger:               zap.NewNop(),
	}

	done := make(chan error, 1)

	go func() {
		done <- exponentialBackoff(opts, action)
	}()

	select {
	case <-time.After(100 * time.Millisecond):
		t.Fatal("exponentialBackoff did not terminate in time")
	case <-done:
		assert.GreaterOrEqual(t, attempts, 3, "Should have retried at least 3 times")
	}
}

func TestExponentialBackoff_GivesUpAfterThreshold(t *testing.T) {
	attempts := 0
	action := func() error {
		attempts++

		return assert.AnError
	}

	opts := backoffOpts{
		InitialBackoff:       1 * time.Millisecond,
		MaxBackoff:           5 * time.Millisecond,
		MaxJitterPct:         0.0,
		SevereErrorThreshold: 3,
		ExponentialFactor:    2.0,
		Logger:               zap.NewNop(),
	}

	err := exponentialBackoff(opts, action)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed")
	assert.Equal(t, 4, attempts, "Should attempt threshold+1 times before giving up")
}

func TestState_AddBackoff_IncrementsFailures(t *testing.T) {
	s := &state{
		backoff:             10 * time.Millisecond,
		consecutiveFailures: 0,
		timer:               time.NewTimer(0),
		opts: backoffOpts{
			InitialBackoff:    1 * time.Millisecond,
			MaxBackoff:        100 * time.Millisecond,
			MaxJitterPct:      0.0,
			ExponentialFactor: 2.0,
			Logger:            zap.NewNop(),
		},
	}
	defer s.timer.Stop()

	s.AddBackoff(1)

	assert.Equal(t, 1, s.consecutiveFailures)
}

func TestState_ResetBackoff_ResetsState(t *testing.T) {
	s := &state{
		backoff:             100 * time.Millisecond,
		consecutiveFailures: 5,
		timer:               time.NewTimer(0),
		opts: backoffOpts{
			InitialBackoff: 10 * time.Millisecond,
			Logger:         zap.NewNop(),
		},
	}
	defer s.timer.Stop()

	s.resetBackoff()

	assert.Equal(t, 0, s.consecutiveFailures)
	assert.Equal(t, 10*time.Millisecond, s.backoff)
}

func TestJitterTime(t *testing.T) {
	base := 100 * time.Millisecond
	maxJitterPct := 0.2

	result := jitterTime(base, maxJitterPct)

	assert.LessOrEqual(t, result, base)
	assert.GreaterOrEqual(t, result, time.Duration(float64(base)*(1-maxJitterPct)))
}
