// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc/connectivity"
)

const (
	maxBufferSize = 2048
	flushInterval = 5 * time.Second
)

type ClientConnInterface interface {
	GetState() connectivity.State
	Close() error
}

// BufferedGrpcWriteSyncer is a custom zap writesync that writes to a grpc stream
// In case stream is not connected it will buffer to memory
type BufferedGrpcWriteSyncer struct {
	client              pb.KubernetesInfoService_SendLogsClient
	conn                ClientConnInterface
	buffer              []string
	mutex               sync.Mutex
	done                chan struct{}
	logger              *zap.Logger
	logLevel            zap.AtomicLevel
	encoder             zapcore.Encoder
	lostLogEntriesCount int
	lostLogEntriesErr   error
}

// NewBufferedGrpcWriteSyncer returns a new BufferedGrpcWriteSyncer
func NewBufferedGrpcWriteSyncer() *BufferedGrpcWriteSyncer {
	bws := &BufferedGrpcWriteSyncer{
		client:              nil,
		conn:                nil,
		buffer:              make([]string, 0, maxBufferSize),
		done:                make(chan struct{}),
		lostLogEntriesCount: 0,
	}
	go bws.run()
	return bws
}

// Close flushes buffered log data into grpc stream if possible, and closes the connection.
func (b *BufferedGrpcWriteSyncer) Close() error {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.flush()
	// Close the channel if not already closed.
	select {
	case <-b.done:
		// Already closed; do nothing
		return nil
	default:
	}
	close(b.done)
	return b.conn.Close()
}

// flush will attempt to dump buffer into GRPC stream if available
func (b *BufferedGrpcWriteSyncer) flush() {
	if len(b.buffer) == 0 || b.conn == nil || b.conn.GetState() != connectivity.Ready {
		return
	}

	if b.lostLogEntriesCount > 0 {
		lostLogsMessage, err := encodeLogEntry(
			b.encoder,
			zapcore.Entry{
				Level:   zap.ErrorLevel,
				Time:    time.Now().UTC(),
				Message: "Lost logs due to buffer overflow",
			},
			[]zap.Field{
				zap.Error(b.lostLogEntriesErr),
				zap.Int("lost_log_entries", b.lostLogEntriesCount),
			},
		)
		if err != nil {
			b.lostLogEntriesErr = err
			return
		}

		if err := b.sendLogEntry(lostLogsMessage); err != nil {
			b.lostLogEntriesErr = err
			return
		}
		b.lostLogEntriesCount = 0
	}

	for _, logEntry := range b.buffer {
		if err := b.sendLogEntry(logEntry); err != nil {
			b.lostLogEntriesCount += 1
			b.lostLogEntriesErr = err
		}
	}
	b.buffer = b.buffer[:0]
}

// run flushes the buffer at the configured interval until Stop is called.
func (b *BufferedGrpcWriteSyncer) run() {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			b.mutex.Lock()
			b.flush()
			b.mutex.Unlock()
		case <-b.done:
			return
		}
	}
}

// encodeLogEntry encodes a log Entry and fields into a string using the given Encoder.
func encodeLogEntry(encoder zapcore.Encoder, logEntry zapcore.Entry, fields []zap.Field) (string, error) {
	buf, err := encoder.EncodeEntry(logEntry, fields)
	if err != nil {
		return "", fmt.Errorf("failed to encode log entry: %w", err)
	}

	// Remove any newline added by Zap's encoder
	buf.TrimNewline()

	return buf.String(), nil
}

// write sends a JSON-encoded log entry, or buffers it if there is no
// connection currently established to the server.
func (b *BufferedGrpcWriteSyncer) write(jsonMessage string) {
	// Do not use logging while locking this mutex to avoid deadlocks
	b.mutex.Lock()
	defer b.mutex.Unlock()

	var shouldBuffer bool

	if b.conn == nil || b.conn.GetState() != connectivity.Ready {
		shouldBuffer = true
	} else {
		// Flush buffered logs
		b.flush()
		if err := b.sendLogEntry(jsonMessage); err != nil {
			shouldBuffer = true
		}
	}

	if shouldBuffer {
		if len(b.buffer) >= maxBufferSize {
			b.lostLogEntriesCount += 1
		}
		b.buffer = append(b.buffer, jsonMessage)
	}
}

// sendLogEntry sends the log encoded into a string to the log server.
func (b *BufferedGrpcWriteSyncer) sendLogEntry(jsonMessage string) error {
	return b.client.Send(&pb.SendLogsRequest{
		Request: &pb.SendLogsRequest_LogEntry{
			LogEntry: &pb.LogEntry{
				JsonMessage: jsonMessage,
			},
		},
	})
}

// UpdateClient updates the gRPC connection and connection in the BufferedGrpcWriteSyncer.
func (b *BufferedGrpcWriteSyncer) UpdateClient(client pb.KubernetesInfoService_SendLogsClient, conn ClientConnInterface) {
	b.mutex.Lock()
	b.client = client
	b.conn = conn
	b.flush()
	b.mutex.Unlock()
}

// ListenToLogStream waits for responses from the server and updates the log level
// based on the contents of responses.
func (b *BufferedGrpcWriteSyncer) ListenToLogStream() error {
	for {
		res, err := b.client.Recv()
		if err == io.EOF {
			// The client has closed the stream
			b.logger.Info("Server closed the SendLogs stream")
			return nil
		}
		if err != nil {
			b.logger.Error("Stream terminated", zap.Error(err))
			return err
		}
		switch res.Response.(type) {
		case *pb.SendLogsResponse_SetLogLevel:
			newLevel := res.GetSetLogLevel().Level
			b.updateLogLevel(newLevel)
		}
	}
}

// updateLogLevel sets the logger's log level based on the response from the server.
func (b *BufferedGrpcWriteSyncer) updateLogLevel(level pb.LogLevel) {
	switch level {
	case pb.LogLevel_LOG_LEVEL_DEBUG:
		b.logger.Info("Set to DEBUG level log")
		b.logLevel.SetLevel(zapcore.DebugLevel)
	case pb.LogLevel_LOG_LEVEL_ERROR:
		b.logger.Info("Set to ERROR level log")
		b.logLevel.SetLevel(zapcore.ErrorLevel)
	case pb.LogLevel_LOG_LEVEL_INFO:
		b.logger.Info("Set to INFO level log")
		b.logLevel.SetLevel(zapcore.InfoLevel)
	case pb.LogLevel_LOG_LEVEL_WARN:
		b.logger.Info("Set to WARN level log")
		b.logLevel.SetLevel(zapcore.WarnLevel)
	default:
		b.logger.Warn("Unknown log level received, defaulting to INFO")
		b.logLevel.SetLevel(zapcore.InfoLevel)
	}
}

// zapCoreWrapper wraps a zapcore.Core to duplicate log entries into a BufferedGrpcWriteSyncer
type zapCoreWrapper struct {
	core       zapcore.Core
	encoder    zapcore.Encoder
	grpcSyncer *BufferedGrpcWriteSyncer
}

var _ zapcore.Core = &zapCoreWrapper{}

func (w *zapCoreWrapper) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if downstream := w.core.Check(entry, ce); downstream != nil {
		return downstream.AddCore(entry, w)
	}
	return ce
}

func (w *zapCoreWrapper) Enabled(level zapcore.Level) bool {
	return w.core.Enabled(level)
}

func (w *zapCoreWrapper) Sync() error {
	return w.core.Sync()
}

func (w *zapCoreWrapper) With(fields []zapcore.Field) zapcore.Core {
	newWrapper := &zapCoreWrapper{
		core:       w.core.With(fields),
		encoder:    w.encoder.Clone(),
		grpcSyncer: w.grpcSyncer,
	}
	for i := range fields {
		fields[i].AddTo(newWrapper.encoder)
	}
	return newWrapper
}

func (w *zapCoreWrapper) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	// Encode the entry immediately and never refer to the Entry and Fields afterwards,
	// as it is more compact and the fields can be garbage-collected while the entry is buffered.
	jsonMessage, err := encodeLogEntry(w.encoder, entry, fields)
	if err != nil {
		return err
	}

	w.grpcSyncer.write(jsonMessage)

	return nil
}

// NewGRPCLogger creates a Zap logger with multiple writesyncs:
// one to stdout and one for GRPC writestream
func NewGRPCLogger(grpcSyncer *BufferedGrpcWriteSyncer, addCaller bool, clock zapcore.Clock) *zap.Logger {
	// Create a production encoder config
	encoderConfig := zap.NewProductionEncoderConfig()

	// Modify the time format to be more human-readable
	encoderConfig.EncodeTime = zapcore.TimeEncoder(func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(t.Format(time.RFC3339))
	})

	// Create a JSON encoder
	encoder := zapcore.NewJSONEncoder(encoderConfig)

	// Create syncers for console output
	consoleSyncer := zapcore.AddSync(os.Stdout)

	// Initialize the atomic level
	atomicLevel := zap.NewAtomicLevelAt(zapcore.InfoLevel)

	consoleCore := zapcore.NewCore(encoder, consoleSyncer, atomicLevel)

	// Create zap logger with the console core
	logger := zap.New(consoleCore,
		zap.WithCaller(addCaller),
		zap.AddStacktrace(zapcore.ErrorLevel),
		zap.WithClock(clock),
		zap.WrapCore(func(core zapcore.Core) zapcore.Core {
			return &zapCoreWrapper{
				core:       core,
				encoder:    encoder,
				grpcSyncer: grpcSyncer,
			}
		}),
	)

	grpcSyncer.logger = logger
	grpcSyncer.logLevel = atomicLevel
	grpcSyncer.encoder = encoder
	return logger
}

// NewProductionGRPCLogger creates a Zap logger configured for production.
func NewProductionGRPCLogger(grpcSyncer *BufferedGrpcWriteSyncer) *zap.Logger {
	return NewGRPCLogger(grpcSyncer, true, zapcore.DefaultClock)
}
