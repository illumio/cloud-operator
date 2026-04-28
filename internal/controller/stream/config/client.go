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

// ConfigurationStream abstracts the GetConfigurationUpdates gRPC stream.
type ConfigurationStream interface {
	Send(req *pb.GetConfigurationUpdatesRequest) error
	Recv() (*pb.GetConfigurationUpdatesResponse, error)
}

// configClient implements stream.StreamClient for the configuration stream.
type configClient struct {
	stream             ConfigurationStream
	logger             *zap.Logger
	verboseDebugging   bool
	bufferedGrpcSyncer *logging.BufferedGrpcWriteSyncer
	stats              *stream.Stats

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

		if err := c.handleConfigUpdate(resp); err != nil {
			return err
		}
	}
}

// handleConfigUpdate processes a configuration update response.
func (c *configClient) handleConfigUpdate(resp *pb.GetConfigurationUpdatesResponse) error {
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

	case *pb.GetConfigurationUpdatesResponse_ResourceData:
		c.logger.Debug("Received configured object data",
			zap.String("id", update.ResourceData.GetId()),
		)

	case *pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete:
		c.logger.Info("Received configured object snapshot complete")

	case *pb.GetConfigurationUpdatesResponse_ResourceMutation:
		mutation := update.ResourceMutation
		switch m := mutation.GetMutation().(type) {
		case *pb.ConfiguredKubernetesObjectMutation_CreateObject:
			c.logger.Debug("Received create object mutation",
				zap.String("id", m.CreateObject.GetId()),
			)
		case *pb.ConfiguredKubernetesObjectMutation_UpdateObject:
			c.logger.Debug("Received update object mutation",
				zap.String("id", m.UpdateObject.GetId()),
			)
		case *pb.ConfiguredKubernetesObjectMutation_DeleteObject:
			c.logger.Debug("Received delete object mutation",
				zap.String("id", m.DeleteObject.GetId()),
			)
		default:
			c.logger.Error("Received unknown configured object mutation, closing stream", zap.Any("response", resp))

			return errors.New("server sent unknown configured object mutation type")
		}

		c.stats.IncrementConfiguredObjectMutations()

	default:
		c.logger.Error("Received unknown configuration update, closing stream", zap.Any("response", resp))

		return errors.New("server sent unknown configuration update type")
	}

	return nil
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
