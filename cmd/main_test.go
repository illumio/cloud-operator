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
		assert.Equal(t, 1, logs.FilterField(zap.String("reason", "flow send blocked")).Len())
	})
}
