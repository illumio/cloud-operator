package controller

import (
	"container/list"
	"context"
	"fmt"
	"time"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

type FlowKey struct {
	// Timestamp is the time the network event occured.
	Timestamp int64 `json:"time"`
	// SrcIP is the source IP address involved in the network event.
	SrcIP string `json:"srcip"`
	// DstIP is the destination IP address involved in the network event.
	DstIP string `json:"dstip"`
	// SrcPort is the source port number involved in the network event.
	SrcPort int `json:"srcport"`
	// DstPort is the destination port number involved in the network event.
	DstPort int `json:"dstport"`
	// Proto is the protocol used in the network event (e.g., TCP, UDP).
	Proto string `json:"proto"`
	// SourceEndpoint contains k8s metadata for source endpoint
	SourceEndpoint string
	// DestinationEndpoint contains k8s metadata for destination endpoint
	DestinationEndpoint string
}

type FlowCache struct {
	cache      map[FlowKey]*list.Element
	queue      *list.List
	bufferSize int
}

func (sm *streamManager) cacheManagerIndefinitely(ctx context.Context) error {
	const activeTimeout = 20 * time.Second

	timer := time.NewTimer(activeTimeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-timer.C:
			expiredCutoff := time.Now().UTC().Add(-activeTimeout)

			for sm.FlowCache.queue.Len() > 0 {
				frontElem := sm.FlowCache.queue.Front()
				switch f := frontElem.Value.(type) {
				case *pb.FalcoFlow:
					flowTimestamp := f.GetTimestamp().AsTime()
					if flowTimestamp.After(expiredCutoff) {
						break // remaining are not expired
					}
					sm.FlowCache.queue.Remove(frontElem)
					flowKey, err := sm.createFlowKey(f)
					delete(sm.FlowCache.cache, flowKey)
					sm.sendNetworkFlowRequest(f)
				case *pb.CiliumFlow:
					return convertCiliumFlowToFlowKey(f), nil
				}
			}

		}

	}
}

// func (f *FlowCache) AddFlow(flow interface{}) {
// 	now := time.Now().UTC()
// 	newFlow := &Flow{
// 		FlowKey:   key,
// 		Timestamp: now,
// 		RawFlow:   flow,
// 	}

// 	if existingElem, found := fc.cache[key]; found {
// 		existingFlow := existingElem.Value.(*Flow)
// 		aggregate(existingFlow, newFlow)
// 		return
// 	}

// 	// Evict if over capacity
// 	if fc.queue.Len() >= fc.bufferSize {
// 		frontElem := fc.queue.Front()
// 		oldest := frontElem.Value.(*Flow)
// 		fc.queue.Remove(frontElem)
// 		delete(fc.cache, oldest.FlowKey)
// 		send(oldest)
// 	}

// 	// Insert at back of queue
// 	elem := fc.queue.PushBack(newFlow)
// 	fc.cache[key] = elem
// }

// // ConvertTimestamp converts a timestamppb.Timestamp to int64
// func ConvertTimestamp(ts *timestamppb.Timestamp) int64 {
// 	return ts.AsTime().Unix()
// }

// func EvictExpiredFlows() {
// 	expiredCutoff := time.Now().UTC().Add(-20 * time.Second)

// 	for fc.queue.Len() > 0 {
// 		frontElem := fc.queue.Front()
// 		flow := frontElem.Value.(*Flow)

// 		if flow.Timestamp.After(expiredCutoff) {
// 			break // remaining are not expired
// 		}

// 		fc.queue.Remove(frontElem)
// 		delete(fc.cache, flow.FlowKey)
// 		send(flow)
// 	}
// }

func (sm *streamManager) createFlowKey(flow interface{}) (FlowKey, error) {
	switch f := flow.(type) {
	case *pb.FalcoFlow:
		return convertFalcoFlowToFlowKey(f), nil
	case *pb.CiliumFlow:
		return convertCiliumFlowToFlowKey(f), nil

	default:
		return FlowKey{}, fmt.Errorf("unsupported flow type: %T", flow)
	}
}

func convertCiliumFlowToFlowKey(flow *pb.CiliumFlow) FlowKey {
	if flow == nil || flow.Layer3 == nil || flow.Layer4 == nil {
		return FlowKey{}
	}

	var (
		timestamp int64
		srcPort   int
		dstPort   int
		proto     string
	)

	// Timestamp
	if flow.Time != nil {
		timestamp = flow.Time.AsTime().Unix()
	}

	// Ports + Protocol
	switch l4 := flow.GetLayer4().GetProtocol().(type) {
	case *pb.Layer4_Tcp:
		srcPort = int(l4.Tcp.GetSourcePort())
		dstPort = int(l4.Tcp.GetDestinationPort())
		proto = "TCP"
	case *pb.Layer4_Udp:
		srcPort = int(l4.Udp.GetSourcePort())
		dstPort = int(l4.Udp.GetDestinationPort())
		proto = "UDP"
	case *pb.Layer4_Sctp:
		srcPort = int(l4.Sctp.GetSourcePort())
		dstPort = int(l4.Sctp.GetDestinationPort())
		proto = "SCTP"
	case *pb.Layer4_Icmpv4:
		proto = "ICMPv4"
	case *pb.Layer4_Icmpv6:
		proto = "ICMPv6"
	default:
		proto = "UNKNOWN"
	}

	return FlowKey{
		Timestamp:           timestamp,
		SrcIP:               flow.Layer3.GetSource(),
		DstIP:               flow.Layer3.GetDestination(),
		SrcPort:             srcPort,
		DstPort:             dstPort,
		Proto:               proto,
		SourceEndpoint:      flow.SourceEndpoint.GetPodName(),
		DestinationEndpoint: flow.DestinationEndpoint.GetPodName(),
	}
}

func convertFalcoFlowToFlowKey(flow *pb.FalcoFlow) FlowKey {
	if flow == nil {
		return FlowKey{}
	}

	srcIP := flow.GetLayer3().GetSource()
	dstIP := flow.GetLayer3().GetDestination()

	var timestamp int64
	if ts := flow.GetTimestamp(); ts != nil {
		timestamp = ts.AsTime().Unix()
	}

	var (
		srcPort int
		dstPort int
		proto   string
	)

	switch l4 := flow.GetLayer4().GetProtocol().(type) {
	case *pb.Layer4_Tcp:
		srcPort = int(l4.Tcp.GetSourcePort())
		dstPort = int(l4.Tcp.GetDestinationPort())
		proto = "TCP"
	case *pb.Layer4_Udp:
		srcPort = int(l4.Udp.GetSourcePort())
		dstPort = int(l4.Udp.GetDestinationPort())
		proto = "UDP"
	case *pb.Layer4_Sctp:
		srcPort = int(l4.Sctp.GetSourcePort())
		dstPort = int(l4.Sctp.GetDestinationPort())
		proto = "SCTP"
	case *pb.Layer4_Icmpv4:
		proto = "ICMPv4"
	case *pb.Layer4_Icmpv6:
		proto = "ICMPv6"
	default:
		proto = "UNKNOWN"
	}

	return FlowKey{
		Timestamp: timestamp,
		SrcIP:     srcIP,
		DstIP:     dstIP,
		SrcPort:   srcPort,
		DstPort:   dstPort,
		Proto:     proto,
	}
}
