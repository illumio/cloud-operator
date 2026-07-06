// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"time"

	observer "github.com/cilium/cilium/api/v1/observer"
	"go.uber.org/zap"
)

// monitorRelayPeerHealth polls Hubble Relay's ServerStatus RPC while a flow
// stream is active and logs how many agent nodes Relay is currently connected
// to. This surfaces a blind spot that is otherwise invisible to the operator.
//
// Relay maintains its own persistent connections to the per-node Cilium agents
// (hubble-peer on :4244) and fans their flows in to every consumer. When those
// relay->agent connections drop and Relay does not reconnect, it keeps the
// operator's GetFlows stream open while delivering no flows: stream.Recv()
// blocks with no error and the operator silently logs flows_received: 0.
//
// The operator cannot repair the relay->agent hop: that connection is managed
// entirely by Relay and is shared across all consumers, so reconnecting the
// operator's own stream would not help. This watchdog is therefore observability
// only. It logs a Warn whenever Relay reports one or more unavailable nodes
// (naming them) so operators and support can immediately distinguish "relay
// healthy but no traffic" from "relay cannot reach its agents", instead of
// investigating a silent flows_received: 0. It returns when ctx is done.
func (fm *CiliumFlowCollector) monitorRelayPeerHealth(ctx context.Context) {
	interval := fm.relayStatusCheckInterval
	if interval <= 0 {
		interval = HubbleRelayStatusCheckInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			connected, unavailable, unavailableNodes, ok := fm.queryRelayPeerStatus(ctx)
			if !ok {
				// The status RPC itself failed; skip this tick. A genuinely dead
				// Relay is handled by the flow stream's own error path.
				continue
			}

			switch {
			case unavailable > 0:
				fm.logger.Warn("Hubble Relay cannot reach some agent nodes; cluster network flows may be incomplete",
					zap.Uint32("connected_nodes", connected),
					zap.Uint32("unavailable_nodes", unavailable),
					zap.Strings("unavailable_node_names", unavailableNodes),
				)
			case connected == 0:
				fm.logger.Warn("Hubble Relay reports zero connected agent nodes; no cluster network flows will be collected until Relay reconnects to its agents",
					zap.Uint32("connected_nodes", connected),
				)
			default:
				fm.logger.Info("Hubble Relay peer status healthy",
					zap.Uint32("connected_nodes", connected),
				)
			}
		}
	}
}

// queryRelayPeerStatus issues a single, timeout-bounded ServerStatus RPC and
// returns the connected/unavailable node counts and unavailable node names. ok
// is false if the status could not be determined.
func (fm *CiliumFlowCollector) queryRelayPeerStatus(ctx context.Context) (connected, unavailable uint32, unavailableNodes []string, ok bool) {
	rpcCtx, cancel := context.WithTimeout(ctx, relayStatusRPCTimeout)
	defer cancel()

	resp, err := fm.client.ServerStatus(rpcCtx, &observer.ServerStatusRequest{})
	if err != nil {
		fm.logger.Debug("Failed to query Hubble Relay ServerStatus", zap.Error(err))

		return 0, 0, nil, false
	}

	return resp.GetNumConnectedNodes().GetValue(),
		resp.GetNumUnavailableNodes().GetValue(),
		resp.GetUnavailableNodes(),
		true
}
