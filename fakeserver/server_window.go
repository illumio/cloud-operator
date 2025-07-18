// Copyright 2025 Illumio, Inc. All Rights Reserved.
// server_window.go is used to send a ServerWindow message to the client.

package main

import (
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"go.uber.org/zap"
)

// ServerWindow represents the rate-limiting window for allowed messages.
type ServerWindow struct {
	AllowedMessages uint32
}

// sendServerWindow sends a ServerWindow message to the client.
func (sw *ServerWindow) sendServerWindow(stream pb.KubernetesInfoService_SendKubernetesNetworkFlowsServer, windowSize uint32, logger *zap.Logger, message string) error {
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
	logger.Info(message, zap.Uint32("allowed_messages", windowSize))
	return nil
}

// SendInitialServerWindow sends the initial ServerWindow message to the client.
func (sw *ServerWindow) SendInitialServerWindow(stream pb.KubernetesInfoService_SendKubernetesNetworkFlowsServer, windowSize uint32, logger *zap.Logger) error {
	return sw.sendServerWindow(stream, windowSize, logger, "Sent initial ServerWindow message")
}

// SendPeriodicServerWindow sends a ServerWindow message to the client if the number of received messages exceeds the window size.
func (sw *ServerWindow) SendPeriodicServerWindow(stream pb.KubernetesInfoService_SendKubernetesNetworkFlowsServer, messagesReceived *uint32, windowSize uint32, logger *zap.Logger) error {
	if *messagesReceived >= windowSize {
		if err := sw.sendServerWindow(stream, windowSize, logger, "Sent periodic ServerWindow message"); err != nil {
			return err
		}
		*messagesReceived = 0
	}
	return nil
}
