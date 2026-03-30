// Copyright 2026 Illumio, Inc. All Rights Reserved.

package logs

import (
	"context"
	"errors"
	"io"
	"time"

	"go.uber.org/zap"

	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// Stream handles the log stream.
func Stream(ctx context.Context, sm *stream.Manager, logger *zap.Logger, keepalivePeriod time.Duration) error {
	errCh := make(chan error, 1)
	defer close(errCh)

	go func() {
		for {
			_, err := sm.Client.LogStream.Recv()
			if errors.Is(err, io.EOF) {
				logger.Info("Server closed the SendLogs stream")

				errCh <- nil

				return
			}

			if err != nil {
				logger.Error("Stream terminated", zap.Error(err))

				errCh <- err

				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			if err != nil {
				return err
			}

			return nil
		}
	}
}

// ConnectAndStream creates sendLogs client and begins the streaming of logs.
func ConnectAndStream(sm *stream.Manager, logger *zap.Logger, keepalivePeriod time.Duration) error {
	logCtx, logCancel := context.WithCancel(context.Background())
	defer logCancel()

	sendLogsStream, err := sm.Client.GrpcClient.SendLogs(logCtx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))

		return err
	}

	sm.Client.LogStream = sendLogsStream
	sm.BufferedGrpcSyncer.UpdateClient(sm.Client.LogStream, sm.Client.Conn)

	err = Stream(logCtx, sm, logger, keepalivePeriod)
	if err != nil {
		logger.Error("Failed to bootup and stream logs", zap.Error(err))

		return err
	}

	return nil
}
