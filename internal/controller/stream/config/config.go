// Copyright 2026 Illumio, Inc. All Rights Reserved.

package config

import (
	"context"
	"errors"
	"io"
	"time"

	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/pkg/timeutil"
)

// Stream handles the configuration update stream.
func Stream(ctx context.Context, sm *stream.Manager, logger *zap.Logger, keepalivePeriod time.Duration) error {
	// Initialize GVR cache for policy enforcement
	if sm.K8sClient != nil {
		if err := InitPolicyGVRCache(sm.K8sClient.GetClientset(), logger); err != nil {
			logger.Warn("Failed to initialize policy GVR cache", zap.Error(err))
		}
	}

	errCh := make(chan error, 1)
	defer close(errCh)

	go func() {
		for {
			resp, err := sm.Client.ConfigurationStream.Recv()
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

			switch update := resp.GetResponse().(type) {
			case *pb.GetConfigurationUpdatesResponse_UpdateConfiguration:
				logger.Info("Received configuration update",
					zap.Stringer("log_level", update.UpdateConfiguration.GetLogLevel()),
				)

				if sm.VerboseDebugging {
					logger.Debug("verboseDebugging is true, setting log level to debug")
					sm.BufferedGrpcSyncer.UpdateLogLevel(pb.LogLevel_LOG_LEVEL_DEBUG)
				} else {
					sm.BufferedGrpcSyncer.UpdateLogLevel(update.UpdateConfiguration.GetLogLevel())
				}

			case *pb.GetConfigurationUpdatesResponse_NetworkPolicyData:
				logger.Info("Received network policy data",
					zap.String("id", update.NetworkPolicyData.GetId()),
				)

			case *pb.GetConfigurationUpdatesResponse_NetworkPolicySnapshotComplete:
				logger.Info("Received network policy snapshot complete")

			case *pb.GetConfigurationUpdatesResponse_NetworkPolicyMutation:
				mutation := update.NetworkPolicyMutation
				switch m := mutation.GetMutation().(type) {
				case *pb.NetworkPolicyMutation_CreatePolicy:
					if err := CreatePolicy(ctx, sm.K8sClient.GetDynamicClient(), logger, m.CreatePolicy); err != nil {
						logger.Error("Failed to create policy", zap.Error(err))
					}
				case *pb.NetworkPolicyMutation_UpdatePolicy:
					// Update = delete + create
					if err := DeletePolicy(ctx, sm.K8sClient.GetDynamicClient(), logger, m.UpdatePolicy); err != nil {
						logger.Error("Failed to delete policy for update", zap.Error(err))
					}

					if err := CreatePolicy(ctx, sm.K8sClient.GetDynamicClient(), logger, m.UpdatePolicy); err != nil {
						logger.Error("Failed to create policy for update", zap.Error(err))
					}
				case *pb.NetworkPolicyMutation_DeletePolicy:
					if err := DeletePolicy(ctx, sm.K8sClient.GetDynamicClient(), logger, m.DeletePolicy); err != nil {
						logger.Error("Failed to delete policy", zap.Error(err))
					}
				}

			default:
				logger.Warn("Received unknown configuration update", zap.Any("response", resp))
			}
		}
	}()

	ticker := time.NewTicker(timeutil.JitterTime(keepalivePeriod, 0.10))
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
			err := sm.SendKeepalive(logger, stream.TypeConfiguration)
			if err != nil {
				return err
			}
		}
	}
}

// ConnectAndStream creates a configuration update stream client and listens for configuration changes.
func ConnectAndStream(sm *stream.Manager, logger *zap.Logger, keepalivePeriod time.Duration) error {
	configCtx, configCancel := context.WithCancel(context.Background())
	defer configCancel()

	getConfigurationUpdatesStream, err := sm.Client.GrpcClient.GetConfigurationUpdates(configCtx)
	if err != nil {
		logger.Error("Failed to connect to server", zap.Error(err))

		return err
	}

	sm.Client.ConfigurationStream = getConfigurationUpdatesStream

	err = Stream(configCtx, sm, logger, keepalivePeriod)
	if err != nil {
		logger.Error("Configuration update stream encountered an error", zap.Error(err))

		return err
	}

	return nil
}
