package k8sclustersyncv1

import (
	"time"
)

var _ Flow = &CiliumFlow{}

type CiliumFlowKey struct {
	SourceIP           string
	DestinationIP      string
	SourcePort         int
	DestinationPort    int
	Protocol           string
	SourceK8sMeta      string
	DestinationK8sMeta string
}

func (flow *CiliumFlow) StartTimestamp() time.Time {
	return flow.GetTime().AsTime()
}

func (flow *CiliumFlow) Key() any {
	if flow == nil {
		return nil
	}

	key := CiliumFlowKey{
		SourceIP:           flow.Layer3.GetSource(),
		DestinationIP:      flow.Layer3.GetDestination(),
		SourceK8sMeta:      flow.SourceEndpoint.GetPodName(),
		DestinationK8sMeta: flow.DestinationEndpoint.GetPodName(),
	}
	// Ports + Protocol
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

	if flow.IsReply.Value {
		return CiliumFlowKey{
			SourceIP:           flow.Layer3.GetDestination(),
			DestinationIP:      flow.Layer3.GetSource(),
			SourcePort:         key.DestinationPort,
			DestinationPort:    key.SourcePort,
			Protocol:           key.Protocol,
			SourceK8sMeta:      flow.DestinationEndpoint.GetPodName(),
			DestinationK8sMeta: flow.SourceEndpoint.GetPodName(),
		}
	}
	return key
}
