// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"io"
	"os"
	"strconv"
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
	client    pb.KubernetesInfoService_SendLogsClient
	conn      *grpc.ClientConn
	buffer    []zapcore.Entry
	mutex     sync.Mutex
	done      chan struct{}
	logger    *zap.SugaredLogger
	logLevel  *zap.AtomicLevel
	encoder   zapcore.Encoder
	lostLogs  bool
	lostBytes int
}

// NewBufferedGrpcWriteSyncer returns a new BufferedGrpcWriteSyncer
func NewBufferedGrpcWriteSyncer(client pb.KubernetesInfoService_SendLogsClient, conn *grpc.ClientConn) *BufferedGrpcWriteSyncer {
	bws := &BufferedGrpcWriteSyncer{
		client:   client,
		conn:     conn,
		buffer:   make([]zapcore.Entry, 0, maxBufferSize),
		done:     make(chan struct{}),
		lostLogs: false,
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

// flush will attempt to dump buffer into GRPC stream if available
func (b *BufferedGrpcWriteSyncer) flush() {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if len(b.buffer) == 0 || b.conn == nil || b.conn.GetState() != connectivity.Ready {
		if len(b.buffer) > maxBufferSize {
			b.lostLogs = true
			b.lostBytes += maxBufferSize - len(b.buffer)
		}
		return
	}

	for _, logEntry := range b.buffer {
		if err := b.sendLog(logEntry); err != nil {
			b.logger.Errorw("Failed to send log",
				"error", err,
				zap.Any("logBufferSize", len(b.buffer)),
			)
			b.lostLogs = true
			b.lostBytes += len(logEntry.Message)
			return
		}
	}
	b.buffer = b.buffer[:0]

	// If previously logs were lost, send a log message to indicate that
	if b.lostLogs {
		lostLogMessage := zapcore.Entry{
			Level:   zapcore.ErrorLevel,
			Time:    time.Now(),
			Message: b.lostLogMessage(),
		}
		err := b.sendLog(lostLogMessage)
		if err != nil {
			b.logger.Warn("Error while sending log",
				"error", err,
			)
		}
		b.lostLogs = false
		b.lostBytes = 0
	}

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
func (b *BufferedGrpcWriteSyncer) sendLog(logEntry zapcore.Entry) error {
	buf, err := b.encoder.EncodeEntry(logEntry, nil)
	if err != nil {
		return err
	}
	err = b.client.Send(&pb.SendLogsRequest{
		Request: &pb.SendLogsRequest_LogEntry{
			LogEntry: &pb.LogEntry{
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

	// If logs were lost, send a log message about it
	if b.conn.GetState() == connectivity.Ready && b.lostLogs {
		lostLogMessage := zapcore.Entry{
			Level:   zapcore.ErrorLevel,
			Time:    time.Now(),
			Message: b.lostLogMessage(),
		}
		err := b.sendLog(lostLogMessage)
		if err != nil {
			b.logger.Warn("Error while sending log",
				"error", err,
			)
		}
		b.lostLogs = false
		b.lostBytes = 0
	}
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
func (b *BufferedGrpcWriteSyncer) bufferLog(entry zapcore.Entry) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	b.buffer = append(b.buffer, entry)
	if len(b.buffer) >= maxBufferSize {
		b.flush()
	}
}

// lostLogMessage generates a message about lost logs
func (b *BufferedGrpcWriteSyncer) lostLogMessage() string {
	return "Lost logs due to buffer overflow. Total lost bytes: " + strconv.Itoa(b.lostBytes)
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
			logger.Error("Error when sending logs to server", zap.Error(err))
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
