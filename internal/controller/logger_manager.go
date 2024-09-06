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
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	maxBufferSize = 100
	flushInterval = 5 * time.Second
)

// BufferedGrpcWriteSyncer is a custom zap writesync that writes to a grpc stream
// In case stream is not connected it will buffer to memory
type BufferedGrpcWriteSyncer struct {
	client   pb.KubernetesInfoService_SendLogsClient
	conn     *grpc.ClientConn
	buffer   []zapcore.Entry
	mutex    sync.Mutex
	done     chan struct{}
	logger   *zap.SugaredLogger
	logLevel *zap.AtomicLevel
	encoder  zapcore.Encoder
}

// NewBufferedGrpcWriteSyncer returns a new BufferedGrpcWriteSyncer
func NewBufferedGrpcWriteSyncer(client pb.KubernetesInfoService_SendLogsClient, conn *grpc.ClientConn) *BufferedGrpcWriteSyncer {
	bws := &BufferedGrpcWriteSyncer{
		client: client,
		conn:   conn,
		buffer: make([]zapcore.Entry, 0, maxBufferSize),
		done:   make(chan struct{}),
	}
	go bws.run()
	return bws
}

// Sync flushes buffered log data into grpc stream if possible.
func (b *BufferedGrpcWriteSyncer) Sync() error {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	b.flush()
	close(b.done)
	return b.conn.Close()
}

// sendLog sends the log as a string to the server.
func (b *BufferedGrpcWriteSyncer) sendLog(logEntry zapcore.Entry) error {
	buf, err := b.encoder.EncodeEntry(logEntry, nil)
	if err != nil {
		return err
	}
	err = b.client.Send(&pb.SendLogsRequest{
		Request: &pb.SendLogsRequest_Log{
			Log: &pb.Log{
				Level:       pb.LogLevel(logEntry.Level),
				Time:        timestamppb.New(logEntry.Time),
				JsonMessage: buf.String(),
			},
		},
	})
	return err
}

// UpdateClient will update BufferedGrpcWriteSyncer with new client stream and GRPC connection
func (b *BufferedGrpcWriteSyncer) UpdateClient(client pb.KubernetesInfoService_SendLogsClient, conn *grpc.ClientConn) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.client = client
	b.conn = conn
}

// ListenToLogStream will wait for responses from server and will update log level
// depending respone contents
func (b *BufferedGrpcWriteSyncer) ListenToLogStream() error {
	for {
		res, err := b.client.Recv()
		if err == io.EOF {
			// The client has closed the stream
			b.logger.Info("Server has closed the SendLogs stream")
			return nil
		}
		if err != nil {
			b.logger.Errorw("Stream terminated",
				"error", err,
			)
			return err // Return the error to terminate the stream
		}
		switch res.Response.(type) {
		case *pb.SendLogsResponse_SetLogLevel:
			newLevel := res.GetSetLogLevel().Level
			b.updateLogLevel(newLevel)
		}
	}
}

// bufferLog adds the log entry to in-memory buffer
func (b *BufferedGrpcWriteSyncer) bufferLog(entry zapcore.Entry) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	b.buffer = append(b.buffer, entry)
	if len(b.buffer) >= maxBufferSize {
		b.flush()
	}
}

// flush will attempt to dump buffer into GRPC stream if available
func (b *BufferedGrpcWriteSyncer) flush() {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if len(b.buffer) == 0 || b.conn == nil || b.conn.GetState() != connectivity.Ready {
		return
	}

	for _, logEntry := range b.buffer {
		if err := b.sendLog(logEntry); err != nil {
			b.logger.Errorw("Failed to send log",
				"error", err,
			)
			return
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
	case pb.LogLevel_LOG_LEVEL_UNSPECIFIED:
		b.logger.Info("Set to UNSPECIFIED level log")
		b.logLevel.SetLevel(zapcore.InfoLevel) // Defaulting to INFO level for unspecified
	default:
		b.logger.Warn("Unknown log level received, defaulting to INFO")
		b.logLevel.SetLevel(zapcore.InfoLevel)
	}
}

// NewGrpclogger will define a new zap logger with multiple writesyncs
// one to stdout and one for GRPC writestream
func NewGrpclogger(grpcSyncer *BufferedGrpcWriteSyncer) *zap.SugaredLogger {
	// Create a development encoder config
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
			grpcSyncer.bufferLog(entry)
			return nil
		}
		if err := grpcSyncer.sendLog(entry); err != nil {
			grpcSyncer.bufferLog(entry)
			return err
		}
		return nil
	}))

	sugaredLogger := logger.Sugar()

	grpcSyncer.logger = sugaredLogger
	grpcSyncer.logLevel = &atomicLevel
	grpcSyncer.encoder = encoder
	return sugaredLogger
}
