// Copyright 2025 Illumio, Inc. All Rights Reserved.
// server_window.go is used to send a ServerWindow message to the client.

package main

import (
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"go.uber.org/zap"
)

type ServerWindow struct {
	AllowedMessages uint32
}

func (sw *ServerWindow) SendServerWindow(stream pb.KubernetesInfoService_SendKubernetesNetworkFlowsServer, messagesReceived *uint32, windowSize uint32, logger *zap.Logger) error {
	if *messagesReceived >= windowSize {
		response := &pb.SendKubernetesNetworkFlowsResponse{
			Response: &pb.SendKubernetesNetworkFlowsResponse_ServerWindow{
				ServerWindow: &pb.ServerWindow{
					AllowedMessages: windowSize,
				},
			},
		}
		if err := stream.Send(response); err != nil {
			logger.Error("Error sending ServerWindow response", zap.Error(err))
			return err
		}
		logger.Info("Sent ServerWindow message", zap.Uint32("allowed_messages", windowSize))
		*messagesReceived = 0 // Reset the counter for the next window
	}
	return nil
}
