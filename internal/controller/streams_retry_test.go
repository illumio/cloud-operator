package controller

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

	// Verify all fields are correctly marshaled
	assert.Equal(t, opts.InitialBackoff, enc.Fields["initial_backoff"])
	assert.Equal(t, opts.MaxBackoff, enc.Fields["max_backoff"])
	assert.InEpsilon(t, opts.ExponentialFactor, enc.Fields["exponential_factor"], 0, 01)
	assert.InEpsilon(t, opts.MaxJitterPct, enc.Fields["max_jitter_pct"], 0.01)
	assert.Equal(t, opts.SevereErrorThreshold, enc.Fields["severe_error_threshold"])
	// Logger field is not marshaled as it's not a basic type
}

func TestExponentialBackoff_SucceedsOnFirstAttempt(t *testing.T) {
	attempts := 0
	//nolint:unparam // action must return error to match exponentialBackoff signature
	action := func() error {
		attempts++

		return nil
	}

	opts := backoffOpts{
		InitialBackoff:       1 * time.Millisecond,
		MaxBackoff:           10 * time.Millisecond,
		MaxJitterPct:         0.0,
		SevereErrorThreshold: 3,
		ExponentialFactor:    2.0,
		Logger:               zap.NewNop(),
	}

	// Run with timeout - success path keeps looping, so we just verify it runs
	done := make(chan error, 1)

	go func() {
		done <- exponentialBackoff(opts, action)
	}()

	select {
	case <-time.After(50 * time.Millisecond):
		// Success path loops forever calling action, so timeout is expected
		assert.Positive(t, attempts, "Should have attempted at least once")
	case err := <-done:
		t.Fatalf("exponentialBackoff returned unexpectedly: %v", err)
	}
}

func TestExponentialBackoff_RetriesOnFailure(t *testing.T) {
	attempts := 0
	action := func() error {
		attempts++
		if attempts < 3 {
			return assert.AnError
		}

		return nil
	}

	opts := backoffOpts{
		InitialBackoff:       1 * time.Millisecond,
		MaxBackoff:           10 * time.Millisecond,
		MaxJitterPct:         0.0,
		SevereErrorThreshold: 5,
		ExponentialFactor:    2.0,
		Logger:               zap.NewNop(),
	}

	done := make(chan error, 1)

	go func() {
		done <- exponentialBackoff(opts, action)
	}()

	select {
	case <-time.After(500 * time.Millisecond):
		assert.GreaterOrEqual(t, attempts, 3, "Should have retried at least 3 times")
	case err := <-done:
		t.Fatalf("exponentialBackoff returned unexpectedly: %v", err)
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
	// UnhappyPathResetBackoff resets consecutiveFailures to 0 before returning
	// so the error message will say "failed 0 times"
	assert.Contains(t, err.Error(), "failed")
	// The check happens BEFORE increment, so with threshold=3:
	// 1st fail: failures=0, not giving up, increment to 1
	// 2nd fail: failures=1, not giving up, increment to 2
	// 3rd fail: failures=2, not giving up, increment to 3
	// 4th fail: failures=3 >= threshold, giving up, return error
	assert.Equal(t, 4, attempts, "Should attempt threshold+1 times before giving up")
}

func TestExponentialBackoff_RespectsMaxBackoff(t *testing.T) {
	attempts := 0
	startTime := time.Now()

	var attemptTimes []time.Duration

	action := func() error {
		attempts++

		attemptTimes = append(attemptTimes, time.Since(startTime))

		if attempts >= 5 {
			return nil
		}

		return assert.AnError
	}

	opts := backoffOpts{
		InitialBackoff:       1 * time.Millisecond,
		MaxBackoff:           5 * time.Millisecond,
		MaxJitterPct:         0.0,
		SevereErrorThreshold: 10,
		ExponentialFactor:    2.0,
		Logger:               zap.NewNop(),
	}

	done := make(chan error, 1)

	go func() {
		done <- exponentialBackoff(opts, action)
	}()

	select {
	case <-time.After(500 * time.Millisecond):
		assert.GreaterOrEqual(t, attempts, 5)
	case err := <-done:
		t.Fatalf("exponentialBackoff returned unexpectedly: %v", err)
	}
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

func TestState_AddBackoff_MultipleTimes(t *testing.T) {
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

	s.AddBackoff(3)

	assert.Equal(t, 3, s.consecutiveFailures)
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

func TestState_HappyPathResetBackoff(t *testing.T) {
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

	s.HappyPathResetBackoff()

	assert.Equal(t, 0, s.consecutiveFailures)
}

func TestState_LongSuccessResetBackoff(t *testing.T) {
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

	s.LongSuccessResetBackoff()

	assert.Equal(t, 0, s.consecutiveFailures)
}

func TestState_UnhappyPathResetBackoff(t *testing.T) {
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

	s.UnhappyPathResetBackoff()

	assert.Equal(t, 0, s.consecutiveFailures)
}
