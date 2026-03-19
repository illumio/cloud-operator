// Copyright 2026 Illumio, Inc. All Rights Reserved.

package controller

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

func TestRealClock_NewTimer(t *testing.T) {
	clock := NewRealClock()

	timer := clock.NewTimer(10 * time.Millisecond)
	defer timer.Stop()

	assert.NotNil(t, timer)
	assert.NotNil(t, timer.C())

	select {
	case <-timer.C():
		// Timer fired as expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timer did not fire in time")
	}
}

func TestRealClock_NewTicker(t *testing.T) {
	clock := NewRealClock()

	ticker := clock.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	assert.NotNil(t, ticker)
	assert.NotNil(t, ticker.C())

	// Wait for at least one tick
	select {
	case <-ticker.C():
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

func TestRealTimer_Stop(t *testing.T) {
	clock := NewRealClock()

	timer := clock.NewTimer(1 * time.Hour)
	stopped := timer.Stop()

	assert.True(t, stopped)
}

func TestRealTimer_Reset(t *testing.T) {
	clock := NewRealClock()

	timer := clock.NewTimer(1 * time.Hour)
	timer.Stop()

	reset := timer.Reset(10 * time.Millisecond)
	assert.False(t, reset) // Timer was already stopped

	select {
	case <-timer.C():
		// Timer fired after reset
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timer did not fire after reset")
	}
}

func TestRealTicker_Stop(t *testing.T) {
	clock := NewRealClock()

	ticker := clock.NewTicker(10 * time.Millisecond)
	ticker.Stop()

	// After stop, ticker should not send more ticks
	// (We can't easily test this without a race, so just verify Stop doesn't panic)
}
