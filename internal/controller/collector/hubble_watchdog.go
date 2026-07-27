// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"sort"
	"time"

	observer "github.com/cilium/cilium/api/v1/observer"
	"go.uber.org/zap"
)

// maxLoggedUnavailableNodes bounds how many unavailable node names are included
// in a log entry. Hubble Relay can report hundreds of unavailable nodes, so the
// list is sorted and truncated to keep log lines readable.
const maxLoggedUnavailableNodes = 10

// monitorHubbleRelayServerStatus polls Hubble Relay's ServerStatus RPC while a
// flow stream is active and logs how many agent nodes Relay is currently
// connected to. This surfaces a blind spot that is otherwise invisible to the
// operator.
//
// Relay maintains its own persistent connections to the per-node Cilium agents
// (hubble-peer on :4244) and fans their flows in to every consumer. When those
// relay->agent connections drop and Relay does not reconnect, it keeps the
// operator's GetFlows stream open while delivering no flows: stream.Recv()
// blocks with no error and the operator silently reports zero flows received.
//
// The operator cannot repair the relay->agent hop: that connection is managed
// entirely by Relay and is shared across all consumers, so reconnecting the
// operator's own stream would not help. This watchdog is therefore observability
// only. Each tick logs a single Info line with the server status counts (so the
// stats can be filtered over time with one message), plus a Warn line when Relay
// cannot reach some or all of its agent nodes. It returns when ctx is done.
func (fm *CiliumFlowCollector) monitorHubbleRelayServerStatus(ctx context.Context) {
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
			numConnectedNodes, numUnavailableNodes, unavailableNodes, ok := fm.queryHubbleRelayServerStatus(ctx)
			if !ok {
				// The status RPC itself failed; skip this tick. A genuinely dead
				// Relay is handled by the flow stream's own error path.
				continue
			}

			loggedNodes := boundUnavailableNodes(unavailableNodes)

			fm.logger.Info("Hubble Relay server status",
				zap.Uint32("numConnectedNodes", numConnectedNodes),
				zap.Uint32("numUnavailableNodes", numUnavailableNodes),
				zap.Strings("unavailableNodes", loggedNodes),
			)

			if numUnavailableNodes > 0 || numConnectedNodes == 0 {
				fm.logger.Warn("Hubble Relay cannot reach some or all of its agent nodes; cluster network flows may be incomplete or missing",
					zap.Uint32("numConnectedNodes", numConnectedNodes),
					zap.Uint32("numUnavailableNodes", numUnavailableNodes),
					zap.Strings("unavailableNodes", loggedNodes),
				)
			}
		}
	}
}

// boundUnavailableNodes returns a sorted copy of nodes truncated to
// maxLoggedUnavailableNodes entries so log lines stay readable when Relay
// reports a large number of unavailable nodes.
func boundUnavailableNodes(nodes []string) []string {
	if len(nodes) == 0 {
		return nodes
	}

	sorted := make([]string, len(nodes))
	copy(sorted, nodes)
	sort.Strings(sorted)

	if len(sorted) > maxLoggedUnavailableNodes {
		return sorted[:maxLoggedUnavailableNodes]
	}

	return sorted
}

// queryHubbleRelayServerStatus issues a single, timeout-bounded ServerStatus RPC
// and returns the connected/unavailable node counts and unavailable node names.
// ok is false if the status could not be determined.
func (fm *CiliumFlowCollector) queryHubbleRelayServerStatus(ctx context.Context) (numConnectedNodes, numUnavailableNodes uint32, unavailableNodes []string, ok bool) {
	rpcCtx, cancel := context.WithTimeout(ctx, relayStatusRPCTimeout)
	defer cancel()

	resp, err := fm.client.ServerStatus(rpcCtx, &observer.ServerStatusRequest{})
	if err != nil {
		fm.logger.Warn("Failed to query Hubble Relay ServerStatus", zap.Error(err))

		return 0, 0, nil, false
	}

	return resp.GetNumConnectedNodes().GetValue(),
		resp.GetNumUnavailableNodes().GetValue(),
		resp.GetUnavailableNodes(),
		true
}
