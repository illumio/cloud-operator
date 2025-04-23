package k8sclustersyncv1

import (
	"time"
)

var _ Flow = &FalcoFlow{}

type FalcoFlowKey struct {
	SourceIP        string
	DestinationIP   string
	SourcePort      int
	DestinationPort int
	Protocol        string
}

func (flow *FalcoFlow) StartTimestamp() time.Time {
	return flow.GetTimestamp().AsTime()
}

func (flow *FalcoFlow) Key() any {
	if flow == nil {
		return nil

	}

	srcIP := flow.GetLayer3().GetSource()
	dstIP := flow.GetLayer3().GetDestination()

	var (
		srcPort int
		dstPort int
		proto   string
	)

	switch l4 := flow.GetLayer4().GetProtocol().(type) {
	case *Layer4_Tcp:
		srcPort = int(l4.Tcp.GetSourcePort())
		dstPort = int(l4.Tcp.GetDestinationPort())
		proto = "TCP"
	case *Layer4_Udp:
		srcPort = int(l4.Udp.GetSourcePort())
		dstPort = int(l4.Udp.GetDestinationPort())
		proto = "UDP"
	case *Layer4_Sctp:
		srcPort = int(l4.Sctp.GetSourcePort())
		dstPort = int(l4.Sctp.GetDestinationPort())
		proto = "SCTP"
	case *Layer4_Icmpv4:
		proto = "ICMPv4"
	case *Layer4_Icmpv6:
		proto = "ICMPv6"
	default:
		proto = "UNKNOWN"
	}

	return FalcoFlowKey{
		SourceIP:        srcIP,
		DestinationIP:   dstIP,
		SourcePort:      srcPort,
		DestinationPort: dstPort,
		Protocol:        proto,
	}
}
