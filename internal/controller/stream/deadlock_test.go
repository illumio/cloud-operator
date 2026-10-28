// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestServerIsHealthy(t *testing.T) {
	// Reset state
	SetProcessingResources(false)
	ConfigureFlowLiveness(0)

	t.Run("healthy when not processing", func(t *testing.T) {
		assert.True(t, ServerIsHealthy())
	})

	t.Run("healthy when processing just started", func(t *testing.T) {
		SetProcessingResources(true)
		assert.True(t, ServerIsHealthy())
		SetProcessingResources(false)
	})

	t.Run("unhealthy when processing for too long", func(t *testing.T) {
		// Manually set the state to simulate long processing
		dd.mutex.Lock()
		dd.processingResources = true
		dd.timeStarted = time.Now().Add(-ResourceProcessingTimeout - time.Minute)
		dd.mutex.Unlock()

		assert.False(t, ServerIsHealthy())
		assert.Contains(t, UnhealthyReason(), "resource processing")

		// Reset
		SetProcessingResources(false)
	})
}

func TestSetProcessingResources(t *testing.T) {
	// Reset state
	SetProcessingResources(false)

	t.Run("sets processing to true", func(t *testing.T) {
		SetProcessingResources(true)

		dd.mutex.RLock()
		processing := dd.processingResources
		dd.mutex.RUnlock()

		assert.True(t, processing)
		SetProcessingResources(false)
	})

	t.Run("sets processing to false", func(t *testing.T) {
		SetProcessingResources(true)
		SetProcessingResources(false)

		dd.mutex.RLock()
		processing := dd.processingResources
		dd.mutex.RUnlock()

		assert.False(t, processing)
	})

	t.Run("updates time when starting processing", func(t *testing.T) {
		before := time.Now()

		SetProcessingResources(true)

		dd.mutex.RLock()
		timeStarted := dd.timeStarted
		dd.mutex.RUnlock()

		assert.True(t, timeStarted.After(before) || timeStarted.Equal(before))
		SetProcessingResources(false)
	})
}

func TestFlowLiveness(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "disabled without a collector",
			run: func(t *testing.T) {
				t.Helper()

				ConfigureFlowLiveness(0)
				StartFlowCollection()
				RecordFlowSent()
				time.Sleep(10 * time.Minute)
				assert.True(t, ServerIsHealthy())
			},
		},
		{
			name: "waits for the collector to start",
			run: func(t *testing.T) {
				t.Helper()

				time.Sleep(10 * time.Minute)
				assert.True(t, ServerIsHealthy())
			},
		},
		{
			name: "first flow has a full timeout of grace",
			run: func(t *testing.T) {
				t.Helper()

				StartFlowCollection()
				time.Sleep(time.Minute)
				assert.True(t, ServerIsHealthy())
				time.Sleep(time.Nanosecond)
				assert.False(t, ServerIsHealthy())
				assert.Contains(t, UnhealthyReason(), "no network flow has been sent for")
			},
		},
		{
			name: "successful flow resets the timer and restores health",
			run: func(t *testing.T) {
				t.Helper()

				StartFlowCollection()
				time.Sleep(time.Minute + time.Nanosecond)
				assert.False(t, ServerIsHealthy())
				RecordFlowSent()
				assert.True(t, ServerIsHealthy())
				time.Sleep(time.Minute)
				assert.True(t, ServerIsHealthy())
				time.Sleep(time.Nanosecond)
				assert.False(t, ServerIsHealthy())
			},
		},
		{
			name: "restarting collection does not reset the timer",
			run: func(t *testing.T) {
				t.Helper()

				StartFlowCollection()
				time.Sleep(30 * time.Second)
				StartFlowCollection()
				time.Sleep(30*time.Second + time.Nanosecond)
				assert.False(t, ServerIsHealthy())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ConfigureFlowLiveness(time.Minute)
				t.Cleanup(func() { ConfigureFlowLiveness(0) })
				// Sleep advances the synthetic clock, without waiting in real time.
				tt.run(t)
			})
		})
	}
}

func TestFlowInactivityTimeout(t *testing.T) {
	tests := []struct {
		name             string
		activeTimeout    time.Duration
		pollInterval     time.Duration
		keepalivePeriods []time.Duration
		want             time.Duration
	}{
		{
			name:             "defaults",
			activeTimeout:    20 * time.Second,
			keepalivePeriods: []time.Duration{10 * time.Second, 10 * time.Second, 10 * time.Second, 10 * time.Second},
			want:             110 * time.Second,
		},
		{
			name:             "includes slow collector polling",
			activeTimeout:    20 * time.Second,
			pollInterval:     5 * time.Minute,
			keepalivePeriods: []time.Duration{10 * time.Second, 10 * time.Second, 10 * time.Second, 10 * time.Second},
			want:             410 * time.Second,
		},
		{
			name:             "uses the shortest keepalive and configured cache timeout",
			activeTimeout:    2 * time.Minute,
			keepalivePeriods: []time.Duration{time.Minute, 5 * time.Second, 30 * time.Second, 10 * time.Second},
			want:             195 * time.Second,
		},
		{
			name:             "ignores nonpositive keepalive periods",
			activeTimeout:    20 * time.Second,
			keepalivePeriods: []time.Duration{0, -time.Second, 10 * time.Second},
			want:             110 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FlowInactivityTimeout(tt.activeTimeout, tt.pollInterval, tt.keepalivePeriods...))
		})
	}
}

func TestFlowLivenessWithSlowPolling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const (
			pollInterval  = 5 * time.Minute
			activeTimeout = 20 * time.Second
		)

		timeout := FlowInactivityTimeout(activeTimeout, pollInterval, 10*time.Second)
		ConfigureFlowLiveness(timeout)
		t.Cleanup(func() { ConfigureFlowLiveness(0) })
		StartFlowCollection()
		RecordFlowSent()

		// A healthy collector can wait a full poll interval, then flush its cache.
		time.Sleep(pollInterval + activeTimeout)
		assert.True(t, ServerIsHealthy())
		RecordFlowSent()
		time.Sleep(pollInterval + activeTimeout)
		assert.True(t, ServerIsHealthy())

		// It must still fail if the next successful send never arrives.
		time.Sleep(timeout - pollInterval - activeTimeout + time.Nanosecond)
		assert.False(t, ServerIsHealthy())
	})
}
