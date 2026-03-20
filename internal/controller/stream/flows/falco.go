// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// StreamFalco handles the falco network flow stream.
func StreamFalco(ctx context.Context, sm *stream.Manager, logger *zap.Logger) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case falcoFlow := <-sm.Client.FalcoEventChan:
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
				logger.Error("Failed to parse Falco event into flow", zap.Error(err))

				return err
			}

			err = sm.FlowCache.CacheFlow(ctx, convertedFiveTupleFlow)
			if err != nil {
				logger.Error("Failed to cache flow", zap.Error(err))

				return err
			}

			sm.Stats.IncrementFlowsReceived()
		}
	}
}

// ConnectAndStreamFalco creates networkFlowsStream client and begins streaming Falco flows.
func ConnectAndStreamFalco(sm *stream.Manager, logger *zap.Logger, _ time.Duration) error {
	falcoCtx, falcoCancel := context.WithCancel(context.Background())
	defer falcoCancel()

	err := StreamFalco(falcoCtx, sm, logger)
	if err != nil {
		logger.Error("Failed to stream Falco network flows", zap.Error(err))

		return err
	}

	return nil
}
