// Copyright 2025 Illumio, Inc. All Rights Reserved.

package controller

import (
	"container/list"
	"context"
	"fmt"
	"time"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"go.uber.org/zap"
)

type FlowKey struct {
	// Timestamp is the time the network event occured.
	Timestamp int64
	// SrcIP is the source IP address involved in the network event.
	SrcIP string
	// DstIP is the destination IP address involved in the network event.
	DstIP string
	// SrcPort is the source port number involved in the network event.
	SrcPort int
	// DstPort is the destination port number involved in the network event.
	DstPort int
	// Proto is the protocol used in the network event (e.g., TCP, UDP).
	Proto string
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

// cacheManagerIndefinitely manages the flow cache by evicting expired flows based on the active timeout,
// processing new flows, and resetting the timer for the next expiration.
func (sm *streamManager) cacheManagerIndefinitely(ctx context.Context, logger *zap.Logger) error {
	const activeTimeout = 20 * time.Second // Define the active timeout period for flows. TODO: make configurable

	// Create a new timer to trigger every activeTimeout duration.
	timer := time.NewTimer(activeTimeout)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done(): // If the context is canceled, exit the loop and return the error.
			return ctx.Err()

		case <-timer.C:
			now := time.Now().UTC()
			expiredCutoff := now.Add(-activeTimeout) // Determine the cutoff time for expired flows.

			// Iterate through the flow cache queue to evict expired flows.
			for sm.FlowCache.queue.Len() > 0 {
				frontElem := sm.FlowCache.queue.Front()
				var flowTimestamp time.Time
				var flowKey FlowKey
				var err error

				// Check the type of flow and get the flow timestamp and flow key.
				switch f := frontElem.Value.(type) {
				case *pb.FalcoFlow:
					flowTimestamp = f.GetTimestamp().AsTime()
					flowKey, err = sm.createFlowKey(f)
				case *pb.CiliumFlow:
					flowTimestamp = f.GetTime().AsTime()
					flowKey, err = sm.createFlowKey(f)
				default:
					break
				}
				if err != nil {
					return err
				}

				// If the flow is not expired (timestamp is newer than the cutoff), stop evicting.
				if flowTimestamp.After(expiredCutoff) {
					break
				}

				// Evict the expired flow by removing it from the cache and queue.
				sm.FlowCache.queue.Remove(frontElem)
				delete(sm.FlowCache.cache, flowKey)

				sm.sendNetworkFlowRequest(logger, frontElem.Value)
			}

			// Reset the timer to expire based on the next flow's timestamp.
			if front := sm.FlowCache.queue.Front(); front != nil {
				switch f := front.Value.(type) {
				case *pb.FalcoFlow:
					resetTimer(timer, f.GetTimestamp().AsTime(), activeTimeout)
				case *pb.CiliumFlow:
					resetTimer(timer, f.GetTime().AsTime(), activeTimeout)
				default:
					timer.Reset(activeTimeout)
				}
			} else {
				// If there are no flows left, reset the timer to the default activeTimeout period.
				timer.Reset(activeTimeout)
			}

		case flow := <-sm.flowChannel:
			flowKey, err := sm.createFlowKey(flow)
			if err != nil {
				return err
			}

			// If the flow already exists in the cache, skip adding it again.
			if _, found := sm.FlowCache.cache[flowKey]; found {
				continue
			}

			// If the cache is full, evict the oldest flow to make room for the new flow.
			// I think bufferSize should be renamed to maxBufferSize or something.
			if sm.FlowCache.queue.Len() >= sm.FlowCache.bufferSize {
				oldestElem := sm.FlowCache.queue.Front()
				sm.FlowCache.queue.Remove(oldestElem)

				// Process the evicted flow.
				switch old := oldestElem.Value.(type) {
				case *pb.FalcoFlow, *pb.CiliumFlow:
					oldKey, err := sm.createFlowKey(old)
					if err == nil {
						delete(sm.FlowCache.cache, oldKey)
						sm.sendNetworkFlowRequest(logger, old)
					}
				}
			}

			// Add the new flow to the cache and queue.
			flowElem := sm.FlowCache.queue.PushBack(flow)
			sm.FlowCache.cache[flowKey] = flowElem

			// Reset the timer based on the new flow's timestamp.
			switch f := flow.(type) {
			case *pb.FalcoFlow:
				resetTimer(timer, f.GetTimestamp().AsTime(), activeTimeout)
			case *pb.CiliumFlow:
				resetTimer(timer, f.GetTime().AsTime(), activeTimeout)
			}
		}
	}
}

func resetTimer(timer *time.Timer, timestamp time.Time, timeout time.Duration) {
	delay := timestamp.Add(timeout).Sub(time.Now())
	// Not sure if I need this
	// if delay <= 0 {
	// 	delay = time.Second
	// }
	timer.Reset(delay)
}

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
