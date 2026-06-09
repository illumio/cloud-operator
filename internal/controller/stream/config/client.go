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
	"github.com/illumio/cloud-operator/internal/convert"
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
	// Local state for THIS stream (not the cache's global state)
	pendingSnapshot := make(map[string]*pb.ConfiguredKubernetesObjectData)
	cloudSecureIDToKey := make(map[string]string) // maps CloudSecure-assigned Id → cache key
	snapshotComplete := false

	c.logger.Debug("Started configuration snapshot ingestion")

	for {
		select {
		case <-ctx.Done():
			// Just return - cache keeps its previous consistent state
			return ctx.Err()
		default:
		}

		resp, err := c.stream.Recv()
		if errors.Is(err, io.EOF) {
			c.logger.Info("Server closed the GetConfigurationUpdates stream")
			// Just return - cache keeps its previous consistent state
			return nil
		}

		if err != nil {
			c.logger.Error("Configuration stream terminated", zap.Error(err))
			// Just return - cache keeps its previous consistent state
			return err
		}

		if err := c.handleConfigUpdate(ctx, resp, pendingSnapshot, cloudSecureIDToKey, &snapshotComplete); err != nil {
			return err
		}
	}
}

// handleConfigUpdate processes a configuration update response.
// snapshotComplete tracks whether this current stream's snapshot has completed.
func (c *configClient) handleConfigUpdate(ctx context.Context, resp *pb.GetConfigurationUpdatesResponse, pendingSnapshot map[string]*pb.ConfiguredKubernetesObjectData, cloudSecureIDToKey map[string]string, snapshotComplete *bool) error {
	switch update := resp.GetResponse().(type) {
	case *pb.GetConfigurationUpdatesResponse_UpdateConfiguration:
		c.handleUpdateConfiguration(update.UpdateConfiguration)

	case *pb.GetConfigurationUpdatesResponse_ResourceData:
		if *snapshotComplete {
			// Return an error to restart the stream, since we are out of sync with server
			return errors.New("server sent ResourceData after snapshot complete")
		}

		key, err := convert.CacheKeyFromObj(update.ResourceData)
		if err != nil {
			c.logger.Warn("Skipping unsupported resource type",
				zap.String("name", update.ResourceData.GetName()),
				zap.Error(err))

			return nil
		}

		cloudSecureIDToKey[update.ResourceData.GetId()] = key
		// Clear the CloudSecure-assigned Id before caching. The cache uses kind/namespace/name
		// keys, and the reconciler compares config vs runtime objects with proto.Equal — runtime
		// objects don't have this field, so leaving it would cause a permanent mismatch.
		update.ResourceData.Id = ""
		pendingSnapshot[key] = update.ResourceData

	case *pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete:
		if *snapshotComplete {
			// Return an error to restart the stream, since we are out of sync with server
			return errors.New("server sent duplicate ResourceSnapshotComplete")
		}

		// Atomically swap pending snapshot into cache
		objectCount := len(pendingSnapshot)

		err := c.cache.ReplaceAll(ctx, pendingSnapshot)
		if err != nil {
			return err
		}

		*snapshotComplete = true

		c.logger.Info("Configuration snapshot complete",
			zap.Int("object_count", objectCount),
		)

	case *pb.GetConfigurationUpdatesResponse_ResourceMutation:
		if !*snapshotComplete {
			// Return an error to restart the stream, since we are out of sync with server
			return errors.New("server sent ResourceMutation before snapshot complete")
		}

		// Translate CloudSecure Id → cache key for mutations before passing to handleMutation.
		var key string

		switch m := update.ResourceMutation.GetMutation().(type) {
		case *pb.ConfiguredKubernetesObjectMutation_CreateOrUpdateObject:
			var err error

			key, err = convert.CacheKeyFromObj(m.CreateOrUpdateObject)
			if err != nil {
				c.logger.Warn("Skipping unsupported create/update mutation",
					zap.String("name", m.CreateOrUpdateObject.GetName()),
					zap.Error(err))

				return nil
			}

			cloudSecureIDToKey[m.CreateOrUpdateObject.GetId()] = key
			// Clear the CloudSecure-assigned Id before caching (see ResourceData comment above).
			m.CreateOrUpdateObject.Id = ""
		case *pb.ConfiguredKubernetesObjectMutation_DeleteObject:
			var ok bool

			key, ok = cloudSecureIDToKey[m.DeleteObject.GetId()]
			if !ok {
				c.logger.Warn("Ignoring delete for unknown CloudSecure Id", zap.String("id", m.DeleteObject.GetId()))

				return nil
			}

			delete(cloudSecureIDToKey, m.DeleteObject.GetId())
		}

		if err := c.handleMutation(ctx, key, update.ResourceMutation); err != nil {
			return err
		}

		c.stats.IncrementConfiguredObjectMutations()

	default:
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
// The cache key is computed by handleConfigUpdate and passed in directly.
func (c *configClient) handleMutation(ctx context.Context, key string, mutation *pb.ConfiguredKubernetesObjectMutation) error {
	switch m := mutation.GetMutation().(type) {
	case *pb.ConfiguredKubernetesObjectMutation_CreateOrUpdateObject:
		if err := c.cache.Insert(ctx, key, m.CreateOrUpdateObject); err != nil {
			return err
		}

		c.logger.Debug("Created or updated configured object",
			zap.String("key", key),
			zap.String("name", m.CreateOrUpdateObject.GetName()),
		)

	case *pb.ConfiguredKubernetesObjectMutation_DeleteObject:
		if err := c.cache.Delete(ctx, key); err != nil {
			return err
		}

		c.logger.Debug("Deleted configured object",
			zap.String("key", key),
		)

	default:
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
