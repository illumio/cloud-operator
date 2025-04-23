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

	var (
		srcPort int
		dstPort int
		proto   string
	)

	// Ports + Protocol
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

	return CiliumFlowKey{
		SourceIP:           flow.Layer3.GetSource(),
		DestinationIP:      flow.Layer3.GetDestination(),
		SourcePort:         srcPort,
		DestinationPort:    dstPort,
		Protocol:           proto,
		SourceK8sMeta:      flow.SourceEndpoint.GetPodName(),
		DestinationK8sMeta: flow.DestinationEndpoint.GetPodName(),
	}
}
