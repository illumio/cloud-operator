// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"errors"
	"io"
	"time"

	"go.uber.org/zap"
)

// StreamLogs handles the log stream.
func (sm *streamManager) StreamLogs(ctx context.Context, logger *zap.Logger, keepalivePeriod time.Duration) error {
	errCh := make(chan error, 1)
	defer close(errCh)

	go func() {
		for {
			_, err := sm.streamClient.logStream.Recv()
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

// connectAndStreamLogs creates sendLogs client and begins the streaming of
// logs. Also starts a goroutine to send keepalives at the configured period.
func (sm *streamManager) connectAndStreamLogs(logger *zap.Logger, keepalivePeriod time.Duration) error {
	logCtx, logCancel := context.WithCancel(context.Background())
	defer logCancel()

	sendLogsStream, err := sm.streamClient.client.SendLogs(logCtx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))

		return err
	}

	sm.streamClient.logStream = sendLogsStream
	sm.bufferedGrpcSyncer.UpdateClient(sm.streamClient.logStream, sm.streamClient.conn)

	err = sm.StreamLogs(logCtx, logger, keepalivePeriod)
	if err != nil {
		logger.Error("Failed to bootup and stream logs", zap.Error(err))

		return err
	}

	return nil
}
