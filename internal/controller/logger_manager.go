// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"os"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8scluster/v1"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	maxBufferSize = 100
	flushInterval = 5 * time.Second
)

type BufferedGrpcWriteSyncer struct {
	client pb.KubernetesInfoService_KubernetesLogsClient
	conn   *grpc.ClientConn
	buffer []string
	mutex  sync.Mutex
	done   chan struct{}
}

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

func (b *BufferedGrpcWriteSyncer) Write(p []byte) (n int, err error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if b.conn.GetState() != connectivity.Ready {
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

func (b *BufferedGrpcWriteSyncer) Sync() error {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	b.flush()
	close(b.done)
	return b.conn.Close()
}

func (b *BufferedGrpcWriteSyncer) flush() {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if len(b.buffer) == 0 || b.conn.GetState() != connectivity.Ready {
		return
	}

	for _, logEntry := range b.buffer {
		if err := b.sendLog(logEntry); err != nil {
			log.Log.Error(err, "Failed to send log")
			return
		}
	}
	b.buffer = b.buffer[:0]
}

func (b *BufferedGrpcWriteSyncer) sendLog(logEntry string) error {
	err := b.client.Send(&pb.KubernetesLogsRequest{Logs: logEntry})
	return err
}

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

func NewGrpclogger(grpcSyncer *BufferedGrpcWriteSyncer) logr.Logger {
	// Create a development encoder config
	encoderConfig := zap.NewDevelopmentEncoderConfig()

	// Create a JSON encoder
	encoder := zapcore.NewJSONEncoder(encoderConfig)

	// Create syncers for console output
	consoleSyncer := zapcore.AddSync(os.Stdout)

	core := zapcore.NewTee(
		zapcore.NewCore(encoder, consoleSyncer, zapcore.InfoLevel),
		zapcore.NewCore(encoder, grpcSyncer, zapcore.InfoLevel),
	)

	// Create a zap logger with the core
	logger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))

	// Convert the zap.Logger to a logr.Logger via zapr
	logrLogger := zapr.NewLogger(logger)

	// Set the controller-runtime logger to the converted logr.Logger
	ctrl.SetLogger(logrLogger)

	return logrLogger
}
