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
	noDump := func(string) {}

	t.Run("healthy", func(t *testing.T) {
		core, logs := observer.New(zap.ErrorLevel)
		rec := httptest.NewRecorder()

		newHealthHandler(zap.New(core), func() string { return "" }, noDump)(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, 0, logs.Len())
	})

	t.Run("unhealthy", func(t *testing.T) {
		core, logs := observer.New(zap.ErrorLevel)
		rec := httptest.NewRecorder()

		newHealthHandler(zap.New(core), func() string { return "flow send blocked" }, noDump)(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), "flow send blocked")
		assert.Equal(t, 1, logs.FilterField(zap.String("reason", "flow send blocked")).Len())
	})

	// The goroutines are dumped when the server becomes unhealthy, and not
	// again by the next probes, which fail until the liveness probe restarts
	// the server.
	t.Run("dumps the goroutines once per unhealthy period", func(t *testing.T) {
		core, logs := observer.New(zap.ErrorLevel)
		reason := ""

		var dumps []string

		handler := newHealthHandler(zap.New(core), func() string { return reason }, func(reason string) {
			dumps = append(dumps, reason)
		})
		probe := func() {
			handler(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))
		}

		probe()
		assert.Empty(t, dumps)

		reason = "flow send blocked"

		probe()
		probe()
		probe()
		assert.Equal(t, 3, logs.FilterMessage("Health check failed").Len())
		assert.Equal(t, []string{"flow send blocked"}, dumps)

		reason = ""

		probe()
		assert.Equal(t, []string{"flow send blocked"}, dumps)

		reason = "resource processing has been running for 5m1s"

		probe()
		assert.Equal(t, []string{"flow send blocked", "resource processing has been running for 5m1s"}, dumps)
	})
}
