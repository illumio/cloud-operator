// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
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
	buffer              []*zapcore.Entry
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
		buffer:              make([]*zapcore.Entry, 0, maxBufferSize),
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
		lostLogEntry := zapcore.Entry{
			Level:   zap.ErrorLevel,
			Time:    time.Now().UTC(),
			Message: "Lost logs due to buffer overflow",
		}

		fields := []zap.Field{
			zap.Error(b.lostLogEntriesErr),
			zap.Int("lost_log_entries", b.lostLogEntriesCount),
		}

		if err := b.sendLog(lostLogEntry, fields); err != nil {
			b.lostLogEntriesErr = err
			return
		}
		b.lostLogEntriesCount = 0
	}

	for _, logEntry := range b.buffer {
		if err := b.sendLog(*logEntry, nil); err != nil {
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

// sendLog sends the log as a string to the server.
func (b *BufferedGrpcWriteSyncer) sendLog(logEntry zapcore.Entry, fields []zap.Field) error {
	buf, err := b.encoder.EncodeEntry(logEntry, fields)
	if err != nil {
		return err
	}
	// Remove the newline added by Zap's encoder
	buf.TrimNewline()
	err = b.client.Send(&pb.SendLogsRequest{
		Request: &pb.SendLogsRequest_LogEntry{
			LogEntry: &pb.LogEntry{
				JsonMessage: buf.String(),
			},
		},
	})
	return err
}

// UpdateClient will update BufferedGrpcWriteSyncer with new client stream and GRPC connection
func (b *BufferedGrpcWriteSyncer) UpdateClient(client pb.KubernetesInfoService_SendLogsClient, conn ClientConnInterface) {
	b.mutex.Lock()
	b.client = client
	b.conn = conn
	b.done = make(chan struct{})
	b.flush()
	b.mutex.Unlock()
}

// ListenToLogStream will wait for responses from server and will update log level
// depending on response contents
func (b *BufferedGrpcWriteSyncer) ListenToLogStream() error {
	for {
		res, err := b.client.Recv()
		if err == io.EOF {
			// The client has closed the stream
			b.logger.Info("Server has closed the SendLogs stream")
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

// bufferLog adds the log entry to in-memory buffer
func (b *BufferedGrpcWriteSyncer) bufferLogEntry(entry zapcore.Entry) {
	if len(b.buffer) >= maxBufferSize {
		b.lostLogEntriesCount += 1
	}
	b.buffer = append(b.buffer, &entry)
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
	return &zapCoreWrapper{
		core:       w.core.With(fields),
		grpcSyncer: w.grpcSyncer,
	}
}

func (w *zapCoreWrapper) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	// Do not use logging inside the hook to avoid deadlock
	w.grpcSyncer.mutex.Lock()
	defer w.grpcSyncer.mutex.Unlock()

	var shouldBuffer bool

	if w.grpcSyncer.conn == nil || w.grpcSyncer.conn.GetState() != connectivity.Ready {
		shouldBuffer = true
	} else {
		// Flush any pending logs
		w.grpcSyncer.flush()
		if err := w.grpcSyncer.sendLog(entry, fields); err != nil {
			shouldBuffer = true
			if shouldBuffer {
				w.grpcSyncer.bufferLogEntry(entry)
			}
			return nil
		}
	}
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
