// Copyright 2026 Illumio, Inc. All Rights Reserved.

package config

import (
	"context"
	"errors"
	"io"
	"sync"

	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/logging"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// Verify configClient implements stream.StreamClient.
var _ stream.StreamClient = (*configClient)(nil)

// configClient implements stream.StreamClient for the configuration stream.
type configClient struct {
	stream             stream.ConfigurationStream
	logger             *zap.Logger
	verboseDebugging   bool
	bufferedGrpcSyncer *logging.BufferedGrpcWriteSyncer

	mutex  sync.RWMutex
	closed bool
}

// Run receives configuration updates from the server until the stream closes.
func (c *configClient) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resp, err := c.stream.Recv()
		if errors.Is(err, io.EOF) {
			c.logger.Info("Server closed the GetConfigurationUpdates stream")

			return nil
		}

		if err != nil {
			c.logger.Error("Configuration stream terminated", zap.Error(err))

			return err
		}

		c.handleConfigUpdate(resp)
	}
}

// handleConfigUpdate processes a configuration update response.
func (c *configClient) handleConfigUpdate(resp *pb.GetConfigurationUpdatesResponse) {
	switch update := resp.GetResponse().(type) {
	case *pb.GetConfigurationUpdatesResponse_UpdateConfiguration:
		c.logger.Info("Received configuration update",
			zap.Stringer("log_level", update.UpdateConfiguration.GetLogLevel()),
		)

		if c.verboseDebugging {
			c.logger.Debug("verboseDebugging is true, setting log level to debug")
			c.bufferedGrpcSyncer.UpdateLogLevel(pb.LogLevel_LOG_LEVEL_DEBUG)
		} else {
			c.bufferedGrpcSyncer.UpdateLogLevel(update.UpdateConfiguration.GetLogLevel())
		}
	default:
		c.logger.Warn("Received unknown configuration update", zap.Any("response", resp))
	}
}

// SendKeepalive sends a keepalive message on the configuration stream.
func (c *configClient) SendKeepalive(_ context.Context) error {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if c.closed {
		return errors.New("stream closed")
	}

	err := c.stream.Send(&pb.GetConfigurationUpdatesRequest{
		Request: &pb.GetConfigurationUpdatesRequest_Keepalive{
			Keepalive: &pb.Keepalive{},
		},
	})
	if err != nil {
		c.logger.Error("Failed to send keepalive on configuration stream", zap.Error(err))

		return err
	}

	return nil
}

// Close marks the client as closed.
func (c *configClient) Close() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.closed = true

	return nil
}
