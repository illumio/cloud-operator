// Copyright 2025 Illumio, Inc. All Rights Reserved.
package controller

import (
	"go.uber.org/zap"
	"io"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// ListenToConfigurationStream listens for configuration updates and applies them dynamically.
func ListenToConfigurationStream(configClient pb.KubernetesInfoService_GetConfigurationUpdatesClient, syncer *BufferedGrpcWriteSyncer) error {
	for {
		// Receive the next configuration update from the stream.
		resp, err := configClient.Recv()
		if err == io.EOF {
			syncer.logger.Info("Configuration update stream closed by remote")
			return nil
		}
		if err != nil {
			syncer.logger.Error("Error receiving configuration update", zap.Error(err))
			return err
		}

		// Process the configuration update based on its type.
		switch update := resp.Response.(type) {
		case *pb.GetConfigurationUpdatesResponse_UpdateConfiguration:
			if update.UpdateConfiguration != nil {
				// Handling log level updates
				syncer.logger.Info("Received log level update",
					zap.Stringer("level", update.UpdateConfiguration.LogLevel),
				)
				syncer.updateLogLevel(update.UpdateConfiguration.LogLevel)
			} else {
				syncer.logger.Warn("Received empty configuration update")
			}

		default:
			syncer.logger.Warn("Received unknown configuration update", zap.Any("response", resp))
		}
	}
}
