package controller

import (
	"io"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// ListenToConfigurationStream listens on the configuration update stream and applies updates.
func ListenToConfigurationStream(configClient pb.KubernetesInfoService_SendConfigurationUpdatesClient, syncer *BufferedGrpcWriteSyncer) error {
	for {
		// Receive the next configuration update from the stream.
		resp, err := configClient.Recv()
		if err == io.EOF {
			syncer.logger.Info("Configuration update stream closed by remote")
			return nil
		}
		if err != nil {
			syncer.logger.Errorw("Error receiving configuration update", "error", err)
			return err
		}

		// Handle the log level update if present.
		if update := resp.GetSetLogLevel(); update != nil {
			syncer.logger.Infow("Received configuration update", "new_level", update.Level)
			// Forward the update to the BufferedGrpcWriteSyncer instance.
			syncer.updateLogLevel(update.Level)
		}
		// Extend here to handle additional configuration updates if needed.
	}
}
