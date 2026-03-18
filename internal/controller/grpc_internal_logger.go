// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"go.uber.org/zap"
	"google.golang.org/grpc/grpclog"
)

// grpcZapLogger adapts a zap.Logger to implement grpclog.LoggerV2.
// This allows GRPC internal logs (connection states, TLS handshakes,
// transport events, retries) to be captured and streamed to k8sclustersync.
type grpcZapLogger struct {
	logger    *zap.Logger
	verbosity int
}

var _ grpclog.LoggerV2 = (*grpcZapLogger)(nil)

// NewGRPCInternalLogger creates a new grpclog.LoggerV2 that wraps the given zap logger.
// The verbosity parameter controls how verbose the GRPC internal logs are (0-2).
func NewGRPCInternalLogger(logger *zap.Logger, verbosity int) grpclog.LoggerV2 {
	return &grpcZapLogger{
		logger:    logger.With(zap.String("component", "grpc")),
		verbosity: verbosity,
	}
}

// Info logs to INFO level.
func (g *grpcZapLogger) Info(args ...any) {
	g.logger.Sugar().Info(args...)
}

// Infoln logs to INFO level.
func (g *grpcZapLogger) Infoln(args ...any) {
	g.logger.Sugar().Info(args...)
}

// Infof logs to INFO level with format.
func (g *grpcZapLogger) Infof(format string, args ...any) {
	g.logger.Sugar().Infof(format, args...)
}

// Warning logs to WARNING level.
func (g *grpcZapLogger) Warning(args ...any) {
	g.logger.Sugar().Warn(args...)
}

// Warningln logs to WARNING level.
func (g *grpcZapLogger) Warningln(args ...any) {
	g.logger.Sugar().Warn(args...)
}

// Warningf logs to WARNING level with format.
func (g *grpcZapLogger) Warningf(format string, args ...any) {
	g.logger.Sugar().Warnf(format, args...)
}

// Error logs to ERROR level.
func (g *grpcZapLogger) Error(args ...any) {
	g.logger.Sugar().Error(args...)
}

// Errorln logs to ERROR level.
func (g *grpcZapLogger) Errorln(args ...any) {
	g.logger.Sugar().Error(args...)
}

// Errorf logs to ERROR level with format.
func (g *grpcZapLogger) Errorf(format string, args ...any) {
	g.logger.Sugar().Errorf(format, args...)
}

// Fatal logs to FATAL level.
func (g *grpcZapLogger) Fatal(args ...any) {
	g.logger.Sugar().Fatal(args...)
}

// Fatalln logs to FATAL level.
func (g *grpcZapLogger) Fatalln(args ...any) {
	g.logger.Sugar().Fatal(args...)
}

// Fatalf logs to FATAL level with format.
func (g *grpcZapLogger) Fatalf(format string, args ...any) {
	g.logger.Sugar().Fatalf(format, args...)
}

// V returns true if the verbosity level is >= the requested level.
// GRPC uses this to control log verbosity (higher = more verbose).
func (g *grpcZapLogger) V(l int) bool {
	return l <= g.verbosity
}

// SetupGRPCInternalLogging configures GRPC to use the provided zap logger
// for all internal logging. This should be called early in main().
func SetupGRPCInternalLogging(logger *zap.Logger, verbosity int) {
	grpcLogger := NewGRPCInternalLogger(logger, verbosity)
	grpclog.SetLoggerV2(grpcLogger)
}
