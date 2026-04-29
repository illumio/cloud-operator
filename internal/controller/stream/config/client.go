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
	"github.com/illumio/cloud-operator/internal/controller/stream/config/cache"
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
	cache              *cache.ConfiguredObjectCache

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
		c.handleUpdateConfiguration(update.UpdateConfiguration)

	case *pb.GetConfigurationUpdatesResponse_ResourceData:
		if c.cache.IsSnapshotComplete() {
			c.logger.Warn("Received ResourceData after snapshot complete, ignoring")

			return nil
		}

		c.cache.Store(update.ResourceData.GetId(), update.ResourceData)
		c.logger.Debug("Stored configured object from snapshot",
			zap.String("id", update.ResourceData.GetId()),
			zap.String("name", update.ResourceData.GetName()),
		)

	case *pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete:
		if c.cache.IsSnapshotComplete() {
			c.logger.Warn("Received duplicate ResourceSnapshotComplete, ignoring")

			return nil
		}

		c.cache.SetSnapshotComplete()
		c.logger.Info("Configuration snapshot complete",
			zap.Int("object_count", c.cache.Len()),
		)

	case *pb.GetConfigurationUpdatesResponse_ResourceMutation:
		if !c.cache.IsSnapshotComplete() {
			c.logger.Warn("Received ResourceMutation before snapshot complete, ignoring")

			return nil
		}

		if err := c.handleMutation(update.ResourceMutation); err != nil {
			return err
		}

		c.stats.IncrementConfiguredObjectMutations()

	default:
		c.logger.Error("Received unknown configuration update, closing stream", zap.Any("response", resp))

		return errors.New("server sent unknown configuration update type")
	}

	return nil
}

// handleUpdateConfiguration processes a log level configuration update.
func (c *configClient) handleUpdateConfiguration(config *pb.GetConfigurationUpdatesResponse_Configuration) {
	c.logger.Info("Received configuration update",
		zap.Stringer("log_level", config.GetLogLevel()),
	)

	if c.verboseDebugging {
		c.logger.Debug("verboseDebugging is true, setting log level to debug")
		c.bufferedGrpcSyncer.UpdateLogLevel(pb.LogLevel_LOG_LEVEL_DEBUG)
	} else {
		c.bufferedGrpcSyncer.UpdateLogLevel(config.GetLogLevel())
	}
}

// handleMutation processes a configured object mutation (create/update/delete).
func (c *configClient) handleMutation(mutation *pb.ConfiguredKubernetesObjectMutation) error {
	switch m := mutation.GetMutation().(type) {
	case *pb.ConfiguredKubernetesObjectMutation_CreateObject:
		c.cache.Store(m.CreateObject.GetId(), m.CreateObject)
		c.logger.Debug("Created configured object",
			zap.String("id", m.CreateObject.GetId()),
			zap.String("name", m.CreateObject.GetName()),
		)

	case *pb.ConfiguredKubernetesObjectMutation_UpdateObject:
		c.cache.Store(m.UpdateObject.GetId(), m.UpdateObject)
		c.logger.Debug("Updated configured object",
			zap.String("id", m.UpdateObject.GetId()),
			zap.String("name", m.UpdateObject.GetName()),
		)

	case *pb.ConfiguredKubernetesObjectMutation_DeleteObject:
		c.cache.Delete(m.DeleteObject.GetId())
		c.logger.Debug("Deleted configured object",
			zap.String("id", m.DeleteObject.GetId()),
		)

	default:
		c.logger.Error("Received unknown configured object mutation, closing stream", zap.Any("mutation", mutation))

		return errors.New("server sent unknown configured object mutation type")
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
