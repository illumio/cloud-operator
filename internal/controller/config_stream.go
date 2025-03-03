package controller

import (
	"io"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// ListenToConfigurationStream listens for configuration updates and applies them dynamically.
func ListenToConfigurationStream(configClient pb.KubernetesInfoService_SendConfigurationUpdatesClient, syncer *BufferedGrpcWriteSyncer) error {
	for {
		// Receive the next configuration update from the stream.
		resp, err := configClient.Recv()
		if err == io.EOF {
			syncer.logger.Info("Configuration update stream closed by remote")
			return nil
		}
		if err != nil {
			syncer.logger.Sugar().Errorw("Error receiving configuration update", "error", err)
			return err
		}

		// Process the configuration update based on its type.
		switch update := resp.Response.(type) {
		case *pb.SendConfigurationUpdatesResponse_SetLogLevel:
			syncer.logger.Sugar().Infow("Received log level update", "new_level", update.SetLogLevel.Level)
			syncer.updateLogLevel(update.SetLogLevel.Level)

		default:
			syncer.logger.Sugar().Warnw("Received unknown configuration update", "response", resp)
		}
	}
}
