package k8sclustersyncv1

import (
	"time"
)

var _ Flow = &FiveTupleFlow{}

// FiveTupleFlowKey identifies a five-tuple flow in the flow cache. The traffic
// direction and verdict are part of the key, so that the reports from both
// sides of a connection are sent, and an allowed flow cannot hide a denied one.
// Collectors that report neither leave them unspecified, so their flows are
// keyed on the five-tuple alone.
type FiveTupleFlowKey struct {
	SourceIP         string
	DestinationIP    string
	SourcePort       int
	DestinationPort  int
	Protocol         string
	TrafficDirection TrafficDirection
	Verdict          Verdict
}

func (flow *FiveTupleFlow) StartTimestamp() time.Time {
	return flow.GetTimestamp().AsTime()
}

func (flow *FiveTupleFlow) Key() any {
	if flow == nil {
		return nil
	}

	key := FiveTupleFlowKey{
		SourceIP:         flow.GetLayer3().GetSource(),
		DestinationIP:    flow.GetLayer3().GetDestination(),
		TrafficDirection: flow.GetTrafficDirection(),
		Verdict:          flow.GetVerdict(),
	}
	key.Protocol, key.SourcePort, key.DestinationPort = layer4Key(flow.GetLayer4())

	return key
}

// layer4Key returns the protocol name and ports used in flow keys.
func layer4Key(layer4 *Layer4) (string, int, int) {
	switch l4 := layer4.GetProtocol().(type) {
	case *Layer4_Tcp:
		return "TCP", int(l4.Tcp.GetSourcePort()), int(l4.Tcp.GetDestinationPort())
	case *Layer4_Udp:
		return "UDP", int(l4.Udp.GetSourcePort()), int(l4.Udp.GetDestinationPort())
	case *Layer4_Sctp:
		return "SCTP", int(l4.Sctp.GetSourcePort()), int(l4.Sctp.GetDestinationPort())
	case *Layer4_Icmpv4:
		return "ICMPv4", 0, 0
	case *Layer4_Icmpv6:
		return "ICMPv6", 0, 0
	default:
		return "UNKNOWN", 0, 0
	}
}
