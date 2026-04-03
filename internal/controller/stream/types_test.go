// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestServerIsHealthy(t *testing.T) {
	// Reset state
	SetProcessingResources(false)

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
