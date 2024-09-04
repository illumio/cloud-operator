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
)

const (
	maxBufferSize = 100
	flushInterval = 5 * time.Second
)

// BufferedGrpcWriteSyncer is a custom zap writesync that writes to a grpc stream
// In case stream is not connected it will buffer to memory
type BufferedGrpcWriteSyncer struct {
	client   pb.KubernetesInfoService_KubernetesLogsClient
	conn     *grpc.ClientConn
	buffer   []string
	mutex    sync.Mutex
	done     chan struct{}
	logger   *zap.SugaredLogger
	logLevel *zap.AtomicLevel
}

// NewBufferedGrpcWriteSyncer returns a new BufferedGrpcWriteSyncer
func NewBufferedGrpcWriteSyncer(client pb.KubernetesInfoService_KubernetesLogsClient, conn *grpc.ClientConn) *BufferedGrpcWriteSyncer {
	bws := &BufferedGrpcWriteSyncer{
		client: client,
		conn:   conn,
		buffer: make([]string, 0, maxBufferSize),
		done:   make(chan struct{}),
	}
	go bws.run()
	return bws
}

// Write writes log data into GRPC, multiple Write calls will be batched,
// and log data will written to into buffer in case GRPC stream is down
func (b *BufferedGrpcWriteSyncer) Write(p []byte) (n int, err error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if b.conn == nil || b.conn.GetState() != connectivity.Ready {
		b.buffer = append(b.buffer, string(p))
		if len(b.buffer) >= maxBufferSize {
			b.flush()
		}
		return len(p), nil
	}

	if err := b.sendLog(string(p)); err != nil {
		b.buffer = append(b.buffer, string(p))
		b.flush()
		return 0, err
	}
	return len(p), nil
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
		return
	}

	for _, logEntry := range b.buffer {
		if err := b.sendLog(logEntry); err != nil {
			b.logger.Error(err, "Failed to send log")
			return
		}
	}
	b.buffer = b.buffer[:0]
}

// sendlog will send log as string to server
func (b *BufferedGrpcWriteSyncer) sendLog(logEntry string) error {
	err := b.client.Send(&pb.KubernetesLogsRequest{Logs: logEntry})
	return err
}

// run flushes the buffer at the configured interval until Stop is
// called.
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

// UpdateClient will update BufferedGrpcWriteSyncer with new client stream and GRPC connection
func (b *BufferedGrpcWriteSyncer) UpdateClient(client pb.KubernetesInfoService_KubernetesLogsClient, conn *grpc.ClientConn) {
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
			b.logger.Info("Server has closed stream")
			return nil
		}
		if err != nil {
			b.logger.Error(err)
			return err // Return the error to terminate the stream
		}
		switch res.Level {
		case pb.LogLevels_LOG_LEVELS_DEBUG:
			b.logger.Info("Set to DEBUG level log")
			b.logLevel.SetLevel(zapcore.DebugLevel)
		case pb.LogLevels_LOG_LEVELS_ERROR:
			b.logger.Info("Set to ERROR level log")
			b.logLevel.SetLevel(zapcore.ErrorLevel)
		case pb.LogLevels_LOG_LEVELS_INFO_UNSPECIFIED:
			b.logger.Info(("Set to INFO level log"))
			b.logLevel.SetLevel(zapcore.InfoLevel)
		case pb.LogLevels_LOG_LEVELS_PANIC:
			b.logger.Info(("Set to PANIC level log"))
			b.logLevel.SetLevel(zapcore.PanicLevel)
		}
	}
}

// NewGrpclogger will define a new zap logger with multiple writesyncs
// one to stdout and one for GRPC writestream
func NewGrpclogger(grpcSyncer *BufferedGrpcWriteSyncer) *zap.SugaredLogger {
	// Create a development encoder config
	encoderConfig := zap.NewDevelopmentEncoderConfig()

	// Create a JSON encoder
	encoder := zapcore.NewJSONEncoder(encoderConfig)

	// Create syncers for console output
	consoleSyncer := zapcore.AddSync(os.Stdout)

	// Initialize the atomic level
	atomicLevel := zap.NewAtomicLevelAt(zapcore.InfoLevel)

	core := zapcore.NewTee(
		zapcore.NewCore(encoder, consoleSyncer, atomicLevel),
		zapcore.NewCore(encoder, grpcSyncer, atomicLevel),
	)

	// Create a zap logger with the core
	logger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1)).Sugar()
	defer logger.Sync()
	grpcSyncer.logger = logger
	grpcSyncer.logLevel = &atomicLevel
	return logger
}
