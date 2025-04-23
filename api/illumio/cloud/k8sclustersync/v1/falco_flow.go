package k8sclustersyncv1

import (
	"time"
)

var _ Flow = &FalcoFlow{}

type FalcoFlowKey struct {
	SourceIP      string
	DestinationIP string
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
		dstPort int
		proto   string
	)

	switch l4 := flow.GetLayer4().GetProtocol().(type) {
	case *Layer4_Tcp:
		dstPort = int(l4.Tcp.GetDestinationPort())
		proto = "TCP"
	case *Layer4_Udp:
		dstPort = int(l4.Udp.GetDestinationPort())
		proto = "UDP"
	case *Layer4_Sctp:
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
		SourceIP:      srcIP,
		DestinationIP: dstIP,
		DestinationPort: dstPort,
		Protocol:        proto,
	}
}
