// Copyright 2026 Illumio, Inc. All Rights Reserved.

package logging

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestGRPCInternalLogger_ImplementsInterface(t *testing.T) {
	logger := zap.NewNop()
	grpcLogger := NewGRPCInternalLogger(logger)

	// Verify it implements grpclog.LoggerV2 by calling methods
	grpcLogger.Info("test")
}

func TestGRPCInternalLogger_Info_DemotedToDebug(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	grpcLogger := NewGRPCInternalLogger(logger)

	grpcLogger.Info("test message")

	if logs.Len() != 1 {
		t.Fatalf("expected 1 log entry, got %d", logs.Len())
	}

	entry := logs.All()[0]

	if entry.Level != zapcore.DebugLevel {
		t.Errorf("expected DEBUG level, got %s", entry.Level)
	}

	if entry.Message != "test message" {
		t.Errorf("expected message 'test message', got '%s'", entry.Message)
	}
}

func TestGRPCInternalLogger_Infof_DemotedToDebug(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	grpcLogger := NewGRPCInternalLogger(logger)

	grpcLogger.Infof("test %s %d", "message", 42)

	if logs.Len() != 1 {
		t.Fatalf("expected 1 log entry, got %d", logs.Len())
	}

	entry := logs.All()[0]

	if entry.Level != zapcore.DebugLevel {
		t.Errorf("expected DEBUG level, got %s", entry.Level)
	}

	if entry.Message != "test message 42" {
		t.Errorf("expected message 'test message 42', got '%s'", entry.Message)
	}
}

func TestGRPCInternalLogger_Warning_MapsToWarn(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	grpcLogger := NewGRPCInternalLogger(logger)

	grpcLogger.Warning("warning message")

	if logs.Len() != 1 {
		t.Fatalf("expected 1 log entry, got %d", logs.Len())
	}

	entry := logs.All()[0]

	if entry.Level != zapcore.WarnLevel {
		t.Errorf("expected WARN level, got %s", entry.Level)
	}

	if entry.Message != "warning message" {
		t.Errorf("expected message 'warning message', got '%s'", entry.Message)
	}
}

func TestGRPCInternalLogger_Warningf_MapsToWarn(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	grpcLogger := NewGRPCInternalLogger(logger)

	grpcLogger.Warningf("warning %s", "formatted")

	if logs.Len() != 1 {
		t.Fatalf("expected 1 log entry, got %d", logs.Len())
	}

	entry := logs.All()[0]

	if entry.Level != zapcore.WarnLevel {
		t.Errorf("expected WARN level, got %s", entry.Level)
	}

	if entry.Message != "warning formatted" {
		t.Errorf("expected message 'warning formatted', got '%s'", entry.Message)
	}
}

func TestGRPCInternalLogger_Error_MapsToError(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	grpcLogger := NewGRPCInternalLogger(logger)

	grpcLogger.Error("error message")

	if logs.Len() != 1 {
		t.Fatalf("expected 1 log entry, got %d", logs.Len())
	}

	entry := logs.All()[0]

	if entry.Level != zapcore.ErrorLevel {
		t.Errorf("expected ERROR level, got %s", entry.Level)
	}

	if entry.Message != "error message" {
		t.Errorf("expected message 'error message', got '%s'", entry.Message)
	}
}

func TestGRPCInternalLogger_Errorf_MapsToError(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	grpcLogger := NewGRPCInternalLogger(logger)

	grpcLogger.Errorf("error %s", "formatted")

	if logs.Len() != 1 {
		t.Fatalf("expected 1 log entry, got %d", logs.Len())
	}

	entry := logs.All()[0]

	if entry.Level != zapcore.ErrorLevel {
		t.Errorf("expected ERROR level, got %s", entry.Level)
	}

	if entry.Message != "error formatted" {
		t.Errorf("expected message 'error formatted', got '%s'", entry.Message)
	}
}

func TestGRPCInternalLogger_VAlwaysReturnsTrue(t *testing.T) {
	logger := zap.NewNop()
	grpcLogger := NewGRPCInternalLogger(logger)

	// V() should always return true since we control visibility via log level
	for i := range 10 {
		if !grpcLogger.V(i) {
			t.Errorf("V(%d) = false, want true", i)
		}
	}
}

func TestGRPCInternalLogger_HasComponentField(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	grpcLogger := NewGRPCInternalLogger(logger)

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
