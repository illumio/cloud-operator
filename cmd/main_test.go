// Copyright 2026 Illumio, Inc. All Rights Reserved.

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestNewHealthHandler(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		core, logs := observer.New(zap.ErrorLevel)
		rec := httptest.NewRecorder()

		newHealthHandler(zap.New(core), func() string { return "" })(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, 0, logs.Len())
	})

	t.Run("unhealthy", func(t *testing.T) {
		core, logs := observer.New(zap.ErrorLevel)
		rec := httptest.NewRecorder()

		newHealthHandler(zap.New(core), func() string { return "flow send blocked" })(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), "flow send blocked")
		assert.Equal(t, 1, logs.FilterMessage("Health check failed").FilterField(zap.String("reason", "flow send blocked")).Len())
	})

	// The goroutines are dumped when the server becomes unhealthy, and not
	// again by the next probes, which fail until the liveness probe restarts
	// the server.
	t.Run("dumps the goroutines once per unhealthy period", func(t *testing.T) {
		core, logs := observer.New(zap.ErrorLevel)
		reason := "flow send blocked"
		handler := newHealthHandler(zap.New(core), func() string { return reason })
		probe := func() {
			handler(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))
		}
		dumps := func() int {
			return logs.FilterMessage("Goroutine dump").FilterField(zap.Int("part", 1)).Len()
		}

		probe()
		probe()
		probe()
		assert.Equal(t, 3, logs.FilterMessage("Health check failed").Len())
		assert.Equal(t, 1, dumps())

		reason = ""

		probe()
		assert.Equal(t, 1, dumps())

		reason = "resource processing has been running for 5m1s"

		probe()
		assert.Equal(t, 2, dumps())
	})
}
