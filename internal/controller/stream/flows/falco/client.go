// Copyright 2026 Illumio, Inc. All Rights Reserved.

package falco

import (
	"context"
	"regexp"
	"sync"

	"go.uber.org/zap"

	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

var reIllumioTraffic = regexp.MustCompile(`\((.*?)\)`)

// Verify falcoClient implements stream.StreamClient.
var _ stream.StreamClient = (*falcoClient)(nil)

// falcoClient implements stream.StreamClient for Falco flow collection.
type falcoClient struct {
	logger         *zap.Logger
	flowCache      *stream.FlowCache
	stats          *stream.Stats
	falcoEventChan chan string

	mutex  sync.RWMutex
	closed bool
}

// Run collects flows from Falco events.
func (c *falcoClient) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case falcoFlow, ok := <-c.falcoEventChan:
			if !ok {
				// Channel closed; exit Run to avoid spinning on an empty receive.
				return nil
			}

			if !collector.FilterIllumioTraffic(falcoFlow) {
				continue
			}

			match := reIllumioTraffic.FindStringSubmatch(falcoFlow)
			if len(match) < 2 {
				continue
			}

			convertedFiveTupleFlow, err := collector.ParsePodNetworkInfo(match[1])
			if convertedFiveTupleFlow == nil {
				continue
			} else if err != nil {
				c.logger.Error("Failed to parse Falco event into flow", zap.Error(err))

				return err
			}

			err = c.flowCache.CacheFlow(ctx, convertedFiveTupleFlow)
			if err != nil {
				c.logger.Error("Failed to cache flow", zap.Error(err))

				return err
			}

			c.stats.IncrementFlowsReceived()
		}
	}
}

// SendKeepalive is a no-op for Falco flow collection (not a gRPC stream).
func (c *falcoClient) SendKeepalive(_ context.Context) error {
	return nil
}

// Close marks the client as closed.
func (c *falcoClient) Close() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.closed = true

	return nil
}
