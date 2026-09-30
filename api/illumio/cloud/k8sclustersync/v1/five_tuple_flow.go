package k8sclustersyncv1

import (
	"time"
)

var _ Flow = &FiveTupleFlow{}

type FiveTupleFlowKey struct {
	SourceIP        string
	DestinationIP   string
	SourcePort      int
	DestinationPort int
	Protocol        string
	// Verdict keeps flows with different verdicts for the same 5-tuple (e.g. an
	// ACCEPT and a DENY) from being deduplicated into one.
	Verdict Verdict
	// TrafficDirection is deliberately not part of the key: the AWS agent logs one
	// connection twice (egress at the source pod, ingress at the destination pod),
	// and keying on it would send both.
}

func (flow *FiveTupleFlow) StartTimestamp() time.Time {
	return flow.GetTimestamp().AsTime()
}

func (flow *FiveTupleFlow) Key() any {
	if flow == nil {
		return nil
	}

	key := FiveTupleFlowKey{
		SourceIP:      flow.GetLayer3().GetSource(),
		DestinationIP: flow.GetLayer3().GetDestination(),
		Verdict:       flow.GetVerdict(),
	}
	switch l4 := flow.GetLayer4().GetProtocol().(type) {
	case *Layer4_Tcp:
		key.SourcePort = int(l4.Tcp.GetSourcePort())
		key.DestinationPort = int(l4.Tcp.GetDestinationPort())
		key.Protocol = "TCP"
	case *Layer4_Udp:
		key.SourcePort = int(l4.Udp.GetSourcePort())
		key.DestinationPort = int(l4.Udp.GetDestinationPort())
		key.Protocol = "UDP"
	case *Layer4_Sctp:
		key.SourcePort = int(l4.Sctp.GetSourcePort())
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
