// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"errors"
	"io"
	"time"

	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// StreamConfigurationUpdates streams configuration updates and applies them dynamically.
func (sm *streamManager) StreamConfigurationUpdates(ctx context.Context, logger *zap.Logger, keepalivePeriod time.Duration) error {
	errCh := make(chan error, 1)
	defer close(errCh)

	go func() {
		for {
			resp, err := sm.streamClient.configStream.Recv()
			if errors.Is(err, io.EOF) {
				logger.Info("Server closed the GetConfigurationUpdates stream")

				errCh <- nil

				return
			}

			if err != nil {
				logger.Error("Stream terminated", zap.Error(err))

				errCh <- err

				return
			}

			// Process the configuration update based on its type.
			switch update := resp.GetResponse().(type) {
			case *pb.GetConfigurationUpdatesResponse_UpdateConfiguration:
				logger.Info("Received configuration update",
					zap.Stringer("log_level", update.UpdateConfiguration.GetLogLevel()),
				)

				if sm.verboseDebugging {
					logger.Debug("verboseDebugging is true, setting log level to debug")
					sm.bufferedGrpcSyncer.UpdateLogLevel(pb.LogLevel_LOG_LEVEL_DEBUG)
				} else {
					sm.bufferedGrpcSyncer.UpdateLogLevel(update.UpdateConfiguration.GetLogLevel())
				}
			default:
				logger.Warn("Received unknown configuration update", zap.Any("response", resp))
			}
		}
	}()

	ticker := time.NewTicker(jitterTime(keepalivePeriod, 0.10))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			if err != nil {
				return err
			}

			return nil
		case <-ticker.C:
			err := sm.sendKeepalive(logger, STREAM_CONFIGURATION)
			if err != nil {
				return err
			}
		}
	}
}

// connectAndStreamConfigurationUpdates creates a configuration update stream client and listens for configuration changes.
func (sm *streamManager) connectAndStreamConfigurationUpdates(logger *zap.Logger, keepalivePeriod time.Duration) error {
	configCtx, configCancel := context.WithCancel(context.Background())
	defer configCancel()

	getConfigurationUpdatesStream, err := sm.streamClient.client.GetConfigurationUpdates(configCtx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))

		return err
	}

	sm.streamClient.configStream = getConfigurationUpdatesStream

	err = sm.StreamConfigurationUpdates(configCtx, logger, keepalivePeriod)
	if err != nil {
		logger.Error("Configuration update stream encountered an error", zap.Error(err))

		return err
	}

	return nil
}
