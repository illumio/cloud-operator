// Copyright 2026 Illumio, Inc. All Rights Reserved.

package logging

import (
	"go.uber.org/zap"
	"google.golang.org/grpc/grpclog"
)

// grpcZapLogger adapts a zap.Logger to implement grpclog.LoggerV2.
// This allows GRPC internal logs (connection states, TLS handshakes,
// transport events, retries) to be captured and streamed to k8sclustersync.
// GRPC Info logs are demoted to DEBUG level to reduce noise.
type grpcZapLogger struct {
	sugar *zap.SugaredLogger
}

var _ grpclog.LoggerV2 = (*grpcZapLogger)(nil)

// NewGRPCInternalLogger creates a new grpclog.LoggerV2 that wraps the given zap logger.
func NewGRPCInternalLogger(logger *zap.Logger) grpclog.LoggerV2 {
	return &grpcZapLogger{
		sugar: logger.With(zap.String("component", "grpc")).Sugar(),
	}
}

// Info logs to DEBUG level (demoted from INFO to reduce noise).
func (g *grpcZapLogger) Info(args ...any) {
	g.sugar.Debug(args...)
}

// Infoln logs to DEBUG level (demoted from INFO to reduce noise).
func (g *grpcZapLogger) Infoln(args ...any) {
	g.sugar.Debug(args...)
}

// Infof logs to DEBUG level (demoted from INFO to reduce noise).
func (g *grpcZapLogger) Infof(format string, args ...any) {
	g.sugar.Debugf(format, args...)
}

// Warning logs to WARNING level.
func (g *grpcZapLogger) Warning(args ...any) {
	g.sugar.Warn(args...)
}

// Warningln logs to WARNING level.
func (g *grpcZapLogger) Warningln(args ...any) {
	g.sugar.Warn(args...)
}

// Warningf logs to WARNING level with format.
func (g *grpcZapLogger) Warningf(format string, args ...any) {
	g.sugar.Warnf(format, args...)
}

// Error logs to ERROR level.
func (g *grpcZapLogger) Error(args ...any) {
	g.sugar.Error(args...)
}

// Errorln logs to ERROR level.
func (g *grpcZapLogger) Errorln(args ...any) {
	g.sugar.Error(args...)
}

// Errorf logs to ERROR level with format.
func (g *grpcZapLogger) Errorf(format string, args ...any) {
	g.sugar.Errorf(format, args...)
}

// Fatal logs to FATAL level.
func (g *grpcZapLogger) Fatal(args ...any) {
	g.sugar.Fatal(args...)
}

// Fatalln logs to FATAL level.
func (g *grpcZapLogger) Fatalln(args ...any) {
	g.sugar.Fatal(args...)
}

// Fatalf logs to FATAL level with format.
func (g *grpcZapLogger) Fatalf(format string, args ...any) {
	g.sugar.Fatalf(format, args...)
}

// V returns true for all verbosity levels.
// Since GRPC Info logs are demoted to DEBUG, we allow all logs
// and let the zap log level control what's displayed.
func (g *grpcZapLogger) V(l int) bool {
	return true
}

// SetupGRPCInternalLogging configures GRPC to use the provided zap logger
// for all internal logging. This should be called early in main().
func SetupGRPCInternalLogging(logger *zap.Logger) {
	grpcLogger := NewGRPCInternalLogger(logger)
	grpclog.SetLoggerV2(grpcLogger)
}
