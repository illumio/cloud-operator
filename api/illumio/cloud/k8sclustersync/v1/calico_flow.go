// Copyright 2026 Illumio, Inc. All Rights Reserved.

package k8sclustersyncv1

import (
	"slices"
	"strings"
	"time"
)

var _ Flow = &CalicoFlow{}

// CalicoFlowKey identifies a Calico flow for deduplication purposes.
type CalicoFlowKey struct {
	// SourceIPs and DestinationIPs are the sorted, comma-joined IP sets.
	SourceIPs      string
	DestinationIPs string
	// SourceName and DestinationName are the aggregated endpoint names.
	SourceName           string
	SourceNamespace      string
	DestinationName      string
	DestinationNamespace string
	DestinationPort      int
	Protocol             string
	Verdict              Verdict
	// TrafficDirection is part of the key, unlike FiveTupleFlowKey: Goldmane reports
	// a connection once from each side, and each report carries only that side's
	// policies, so both must be sent.
	TrafficDirection TrafficDirection
}

// StartTimestamp returns the start time of the flow's aggregation window.
func (flow *CalicoFlow) StartTimestamp() time.Time {
	return flow.GetTime().AsTime()
}

// Key returns a comparable key for this flow used for deduplication.
func (flow *CalicoFlow) Key() any {
	if flow == nil {
		return nil
	}

	key := CalicoFlowKey{
		SourceIPs:            joinSorted(flow.GetSourceIps()),
		DestinationIPs:       joinSorted(flow.GetDestinationIps()),
		SourceName:           flow.GetSourceEndpoint().GetAggregatedName(),
		SourceNamespace:      flow.GetSourceEndpoint().GetNamespace(),
		DestinationName:      flow.GetDestinationEndpoint().GetAggregatedName(),
		DestinationNamespace: flow.GetDestinationEndpoint().GetNamespace(),
		Verdict:              flow.GetVerdict(),
		TrafficDirection:     flow.GetTrafficDirection(),
	}

	switch l4 := flow.GetLayer4().GetProtocol().(type) {
	case *Layer4_Tcp:
		key.DestinationPort = int(l4.Tcp.GetDestinationPort())
		key.Protocol = "TCP"
	case *Layer4_Udp:
		key.DestinationPort = int(l4.Udp.GetDestinationPort())
		key.Protocol = "UDP"
	case *Layer4_Sctp:
		key.DestinationPort = int(l4.Sctp.GetDestinationPort())
		key.Protocol = "SCTP"
	case *Layer4_Icmpv4:
		key.Protocol = "ICMPv4"
	case *Layer4_Icmpv6:
		key.Protocol = "ICMPv6"
	default:
		key.Protocol = "UNKNOWN"
	}

	return key
}

// joinSorted returns the values sorted and comma-joined, without modifying the input.
func joinSorted(values []string) string {
	sorted := slices.Clone(values)
	slices.Sort(sorted)

	return strings.Join(sorted, ",")
}
