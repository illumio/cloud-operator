// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRealClock_Now(t *testing.T) {
	clock := NewRealClock()

	before := time.Now()
	result := clock.Now()
	after := time.Now()

	assert.True(t, result.After(before) || result.Equal(before))
	assert.True(t, result.Before(after) || result.Equal(after))
}

func TestRealClock_Since(t *testing.T) {
	clock := NewRealClock()

	past := time.Now().Add(-1 * time.Second)
	duration := clock.Since(past)

	assert.GreaterOrEqual(t, duration, 1*time.Second)
}

func TestRealClock_NewTicker(t *testing.T) {
	clock := NewRealClock()

	ticker := clock.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	assert.NotNil(t, ticker)

	select {
	case <-ticker.C:
		// Ticker ticked as expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Ticker did not tick in time")
	}
}

func TestRealClock_After(t *testing.T) {
	clock := NewRealClock()

	ch := clock.After(10 * time.Millisecond)

	select {
	case <-ch:
		// Channel received as expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("After channel did not fire in time")
	}
}
