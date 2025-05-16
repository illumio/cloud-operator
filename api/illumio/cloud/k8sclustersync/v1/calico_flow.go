package k8sclustersyncv1

import (
	"time"
)

var _ Flow = &CalicoFlow{}

type CalicoFlowKey struct {
	SourceIP        string
	DestinationIP   string
	SourcePort      int
	DestinationPort int
	Protocol        string
	SourceEndpoint  string
	DestEndpoint    string
}

func (flow *CalicoFlow) StartTimestamp() time.Time {
	return flow.GetTime().AsTime()
}

func (flow *CalicoFlow) Key() any {
	if flow == nil {
		return nil
	}

	key := CalicoFlowKey{
		SourceIP:        flow.GetLayer3().GetSource(),
		DestinationIP:   flow.GetLayer3().GetDestination(),
		SourceEndpoint:  flow.GetSourceEndpoint().GetPodName(),
		DestEndpoint:    flow.GetDestinationEndpoint().GetPodName(),
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

	if flow.GetIsReply().GetValue() {
		key = CalicoFlowKey{
			SourceIP:        key.DestinationIP,
			DestinationIP:   key.SourceIP,
			SourcePort:      key.DestinationPort,
			DestinationPort: key.SourcePort,
			Protocol:        key.Protocol,
			SourceEndpoint:  key.DestEndpoint,
			DestEndpoint:    key.SourceEndpoint,
		}
	}
	return key
}