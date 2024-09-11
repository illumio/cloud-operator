// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"io"
	"os"
	"sync"
	"time"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8scluster/v1"
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
	client         pb.KubernetesInfoService_SendLogsClient
	conn           ClientConnInterface
	buffer         []*zapcore.Entry
	mutex          sync.Mutex
	done           chan struct{}
	logger         *zap.SugaredLogger
	logLevel       zap.AtomicLevel
	encoder        zapcore.Encoder
	lostLogEntries int
	lostLogsErr    error
}

// NewBufferedGrpcWriteSyncer returns a new BufferedGrpcWriteSyncer
func NewBufferedGrpcWriteSyncer() *BufferedGrpcWriteSyncer {
	bws := &BufferedGrpcWriteSyncer{
		client:         nil,
		conn:           nil,
		buffer:         make([]*zapcore.Entry, 0, maxBufferSize),
		done:           make(chan struct{}),
		lostLogEntries: 0,
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
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if len(b.buffer) == 0 || b.conn == nil || b.conn.GetState() != connectivity.Ready {
		return
	}

	if b.lostLogEntries > 0 {
		lostLogEntry := &zapcore.Entry{
			Level:   zap.ErrorLevel,
			Time:    time.Now(),
			Message: "Lost logs due to buffer overflow",
		}

		// Additional fields
		extraFields := []zap.Field{
			zap.Error(b.lostLogsErr),
			zap.Int("lost_log_entries", b.lostLogEntries),
		}

		if err := b.sendLog(lostLogEntry, extraFields); err != nil {
			b.lostLogsErr = err
			return
		}
		b.lostLogEntries = 0
	}

	for _, logEntry := range b.buffer {
		if err := b.sendLog(logEntry, nil); err != nil {
			b.lostLogEntries += 1
			b.lostLogsErr = err
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
			b.flush()
		case <-b.done:
			return
		}
	}
}

// sendLog sends the log as a string to the server.
func (b *BufferedGrpcWriteSyncer) sendLog(logEntry *zapcore.Entry, extraFields []zap.Field) error {
	buf, err := b.encoder.EncodeEntry(*logEntry, extraFields)
	if err != nil {
		return err
	}
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
	b.mutex.Unlock()
	b.flush()
}

// ListenToLogStream will wait for responses from server and will update log level
// depending respone contents
func (b *BufferedGrpcWriteSyncer) ListenToLogStream() {
	for {
		res, err := b.client.Recv()
		if err == io.EOF {
			// The client has closed the stream
			b.logger.Info("Server has closed the SendLogs stream")
			return
		}
		if err != nil {
			b.logger.Errorw("Stream terminated",
				"error", err,
			)
			return
		}
		switch res.Response.(type) {
		case *pb.SendLogsResponse_SetLogLevel:
			newLevel := res.GetSetLogLevel().Level
			b.updateLogLevel(newLevel)
		}
	}
}

// bufferLog adds the log entry to in-memory buffer
func (b *BufferedGrpcWriteSyncer) bufferLogEntry(entry *zapcore.Entry) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	b.buffer = append(b.buffer, entry)
	if len(b.buffer) >= maxBufferSize {
		b.flush()
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

// NewGRPClogger will define a new zap logger with multiple writesyncs
// one to stdout and one for GRPC writestream
func NewGRPClogger(grpcSyncer *BufferedGrpcWriteSyncer) *zap.SugaredLogger {
	// Create a production encoder config
	encoderConfig := zap.NewProductionEncoderConfig()

	// Create a JSON encoder
	encoder := zapcore.NewJSONEncoder(encoderConfig)

	// Create syncers for console output
	consoleSyncer := zapcore.AddSync(os.Stdout)

	// Initialize the atomic level
	atomicLevel := zap.NewAtomicLevelAt(zapcore.InfoLevel)

	consoleCore := zapcore.NewCore(encoder, consoleSyncer, atomicLevel)

	// Create zap logger with the console core
	logger := zap.New(consoleCore, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))

	// Add a custom hook to handle logs for grpcSyncer
	logger = logger.WithOptions(zap.Hooks(func(entry zapcore.Entry) error {
		if grpcSyncer.conn == nil || grpcSyncer.conn.GetState() != connectivity.Ready {
			grpcSyncer.bufferLogEntry(&entry)
			return nil
		}
		if err := grpcSyncer.sendLog(&entry, nil); err != nil {
			logger.Error("Error when sending logs to server", zap.Error(err))
			grpcSyncer.bufferLogEntry(&entry)
			return err
		}
		return nil
	}))

	sugaredLogger := logger.Sugar()

	grpcSyncer.logger = sugaredLogger
	grpcSyncer.logLevel = atomicLevel
	grpcSyncer.encoder = encoder
	return sugaredLogger
}
