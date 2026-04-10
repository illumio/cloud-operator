// Copyright 2026 Illumio, Inc. All Rights Reserved.

package falco

import (
	"context"
	"regexp"

	"go.uber.org/zap"

	"github.com/illumio/cloud-operator/internal/controller/collector"
)

var reIllumioTraffic = regexp.MustCompile(`\((.*?)\)`)

// falcoClient implements FlowCollector for Falco flow collection.
type falcoClient struct {
	logger   *zap.Logger
	flowSink collector.FlowSink
}

// Run collects flows from Falco events.
func (c *falcoClient) Run(ctx context.Context) error {
	// Create channel and start HTTP server locally (like OVN-K's IPFIX collector)
	falcoEventChan := make(chan string)

	server := StartServer(ctx, c.logger, falcoEventChan)
	defer server.Shutdown(ctx) //nolint:errcheck

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case falcoFlow, ok := <-falcoEventChan:
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

			err = c.flowSink.CacheFlow(ctx, convertedFiveTupleFlow)
			if err != nil {
				c.logger.Error("Failed to cache flow", zap.Error(err))

				return err
			}

			c.flowSink.IncrementFlowsReceived()
		}
	}
}
