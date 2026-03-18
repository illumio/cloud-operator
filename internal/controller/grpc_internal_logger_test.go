// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc/grpclog"
)

func TestGRPCInternalLogger_ImplementsInterface(t *testing.T) {
	logger := zap.NewNop()
	grpcLogger := NewGRPCInternalLogger(logger, 2)

	// Verify it implements grpclog.LoggerV2
	var _ grpclog.LoggerV2 = grpcLogger
}

func TestGRPCInternalLogger_Info(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)
	grpcLogger := NewGRPCInternalLogger(logger, 2)

	grpcLogger.Info("test message")

	if logs.Len() != 1 {
		t.Errorf("expected 1 log entry, got %d", logs.Len())
	}
}

func TestGRPCInternalLogger_Infof(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)
	grpcLogger := NewGRPCInternalLogger(logger, 2)

	grpcLogger.Infof("test %s %d", "message", 42)

	if logs.Len() != 1 {
		t.Errorf("expected 1 log entry, got %d", logs.Len())
	}
}

func TestGRPCInternalLogger_Warning(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)
	grpcLogger := NewGRPCInternalLogger(logger, 2)

	grpcLogger.Warning("warning message")

	if logs.Len() != 1 {
		t.Errorf("expected 1 log entry, got %d", logs.Len())
	}
}

func TestGRPCInternalLogger_Warningf(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)
	grpcLogger := NewGRPCInternalLogger(logger, 2)

	grpcLogger.Warningf("warning %s", "message")

	if logs.Len() != 1 {
		t.Errorf("expected 1 log entry, got %d", logs.Len())
	}
}

func TestGRPCInternalLogger_Error(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	logger := zap.New(core)
	grpcLogger := NewGRPCInternalLogger(logger, 2)

	grpcLogger.Error("error message")

	if logs.Len() != 1 {
		t.Errorf("expected 1 log entry, got %d", logs.Len())
	}
}

func TestGRPCInternalLogger_Errorf(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	logger := zap.New(core)
	grpcLogger := NewGRPCInternalLogger(logger, 2)

	grpcLogger.Errorf("error %s", "message")

	if logs.Len() != 1 {
		t.Errorf("expected 1 log entry, got %d", logs.Len())
	}
}

func TestGRPCInternalLogger_Verbosity(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name           string
		configuredVerb int
		requestedVerb  int
		expected       bool
	}{
		{"verbosity 0, request 0", 0, 0, true},
		{"verbosity 0, request 1", 0, 1, false},
		{"verbosity 1, request 0", 1, 0, true},
		{"verbosity 1, request 1", 1, 1, true},
		{"verbosity 1, request 2", 1, 2, false},
		{"verbosity 2, request 2", 2, 2, true},
		{"verbosity 2, request 3", 2, 3, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grpcLogger := NewGRPCInternalLogger(logger, tt.configuredVerb)
			if got := grpcLogger.V(tt.requestedVerb); got != tt.expected {
				t.Errorf("V(%d) = %v, want %v", tt.requestedVerb, got, tt.expected)
			}
		})
	}
}

func TestGRPCInternalLogger_HasComponentField(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)
	grpcLogger := NewGRPCInternalLogger(logger, 2)

	grpcLogger.Info("test")

	if logs.Len() != 1 {
		t.Fatalf("expected 1 log entry, got %d", logs.Len())
	}

	entry := logs.All()[0]
	found := false
	for _, field := range entry.Context {
		if field.Key == "component" && field.String == "grpc" {
			found = true
			break
		}
	}

	if !found {
		t.Error("expected log entry to have component=grpc field")
	}
}
