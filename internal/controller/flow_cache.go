// Copyright 2025 Illumio, Inc. All Rights Reserved.

package controller

import (
	"container/list"
	"context"
	"errors"
	"io"
	"time"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"go.uber.org/zap"
)

type FlowKey interface {
	// Timestamp is the time the network event occured.
	Timestamp() int64
	// SrcIP is the source IP address involved in the network event.
	SrcIP() string
	// DstIP is the destination IP address involved in the network event.
	DstIP() string
	// SrcPort is the source port number involved in the network event.
	SrcPort() int
	// DstPort is the destination port number involved in the network event.
	DstPort() int
	// Proto is the protocol used in the network event (e.g., TCP, UDP).
	Proto() string
	// SourceEndpoint contains k8s metadata for source endpoint
	SourceEndpoint() string
	// DestinationEndpoint contains k8s metadata for destination endpoint
	DestinationEndpoint() string
}

type Flow struct {
	Key       FlowKey
	Timestamp time.Time
	rawFlow   any
}

// FlowCache caches flows to be exported.
// Evicts flows from the cache to be send to the collector for any of the following reasons:
// - lack of resources: the cache has reached the maximum capacity configured in maxFlows
// - active timeout: the flow has been cache longer than the configured activeTimeout
// See https://www.rfc-editor.org/rfc/rfc5102.html#section-5.11.3 for the definition of those reasons.
type FlowCache struct {
	// cache is the set of aggregated flows indexed by their flow keys.
	cache map[FlowKey]Flow
	// queue is the list of cached flows contained in cache, ordered by Timestamp.
	queue *list.List
	// activeTimeout is the maximum duration any flow remains in the cache.
	activeTimeout time.Duration
	// maxFlows is the maximum number of flows cached at any time.
	maxFlows int
	// inFlows contains flows to be aggregated and cached.
	inFlows chan Flow
	// outFlows contains flows that have been evicted from the cache.
	outFlows chan Flow
}

var _ io.Closer = &FlowCache{}

func NewFlowCache(
	activeTimeout time.Duration,
	maxFlows int,
	outFlows chan Flow,
) *FlowCache {
	return &FlowCache{
		cache:         make(map[FlowKey]Flow, maxFlows),
		queue:         list.New(),
		activeTimeout: activeTimeout,
		maxFlows:      maxFlows,
		inFlows:       make(chan Flow, 1),
		outFlows:      outFlows,
	}
}

// Close closes this flow cache's channels.
// This method must be called exactly once on every FlowCache after use.
func (c *FlowCache) Close() error {
	close(c.inFlows)
	close(c.outFlows)
	return nil
}

// CacheFlow aggregates and caches the given flow.
func (c *FlowCache) CacheFlow(ctx context.Context, flow Flow) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case c.inFlows <- flow:
		return nil
	}
}

// cacheManagerIndefinitely manages the flow cache by evicting expired flows based on the active timeout,
// processing new flows, and resetting the timer for the next expiration.
func (c *FlowCache) Run(ctx context.Context, logger *zap.Logger) error {
	// Create a new timer to trigger every activeTimeout duration.
	timer := time.NewTimer(c.activeTimeout)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done(): // If the context is canceled, exit the loop and return the error.
			return ctx.Err()

		case <-timer.C:
			now := time.Now().UTC()
			activeTimeoutCutoff := now.Sub(time.Now().Add(c.activeTimeout)) // Determine the cutoff time for expired flows.

			// Iterate through the flow cache queue to evict expired flows.
			for c.queue.Len() > 0 {
				frontElem := c.queue.Front()
				var flowTimestamp time.Time
				var flowKey FlowKey
				// Check the type of flow and get the flow timestamp and flow key.
				switch f := frontElem.Value.(type) {
				case *pb.FalcoFlow:
					flowTimestamp = f.GetTimestamp().AsTime()
				case *pb.CiliumFlow:
					flowTimestamp = f.GetTime().AsTime()
				default:
					return errors.New("Non supported flow.")
				}

				// If the flow is not expired (timestamp is newer than the cutoff), stop evicting.
				if flowTimestamp.After(time.Now().Add(activeTimeoutCutoff)) {
					break
				}

				// Evict the expired flow by removing it from the cache and queue.
				c.queue.Remove(frontElem)
				delete(c.cache, flowKey)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case c.outFlows <- frontElem.Value.(Flow):
				}
			}

			// Reset the timer to expire based on the next flow's timestamp.
			if front := c.queue.Front(); front != nil {
				switch f := front.Value.(type) {
				case *pb.FalcoFlow:
					resetTimer(timer, f.GetTimestamp().AsTime(), c.activeTimeout)
				case *pb.CiliumFlow:
					resetTimer(timer, f.GetTime().AsTime(), c.activeTimeout)
				default:
					return errors.New("Non supported flow.")
				}
			}
		case flow := <-c.inFlows:
			// If the flow already exists in the cache, skip adding it again.
			if _, found := c.cache[flow.Key]; found {
				continue
			}

			// If the cache is full, evict the oldest flow to make room for the new flow.
			if len(c.cache) >= c.maxFlows {
				oldestFlow := c.queue.Front()
				c.queue.Remove(oldestFlow)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case c.outFlows <- oldestFlow.Value.(Flow):
				}
			}

			// Add the new flow to the cache and queue.
			c.queue.PushBack(flow)
			c.cache[flow.Key] = flow

			oldestFlow := c.queue.Front().Value.(Flow)
			// Reset the timer based on the oldest flow's timestamp (queue is ordered so the front element is oldest).
			switch f := oldestFlow.rawFlow.(type) {
			case *pb.FalcoFlow:
				resetTimer(timer, f.GetTimestamp().AsTime(), c.activeTimeout)
			case *pb.CiliumFlow:
				resetTimer(timer, f.GetTime().AsTime(), c.activeTimeout)
			}
		}
	}
}

func resetTimer(timer *time.Timer, timestamp time.Time, timeout time.Duration) {
	delay := timestamp.Add(timeout).Sub(time.Now())
	timer.Reset(delay)
}

func (c *FlowCache) createFlowKey(flow interface{}) (FlowKey, error) {
	switch f := flow.(type) {
	case *pb.FalcoFlow:
		return convertFalcoFlowToFlowKey(f), nil
	case *pb.CiliumFlow:
		return convertCiliumFlowToFlowKey(f), nil
	}
	return nil, nil
}

func convertCiliumFlowToFlowKey(flow *pb.CiliumFlow) FlowKey {
	if flow == nil || flow.Layer3 == nil || flow.Layer4 == nil {
		return FlowKeyCilium{}
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

	return FlowKeyCilium{
		Ts:                 timestamp,
		SourceIP:           flow.Layer3.GetSource(),
		DestinationIP:      flow.Layer3.GetDestination(),
		SourcePort:         srcPort,
		DestinationPort:    dstPort,
		Protocol:           proto,
		SourceK8sMeta:      flow.SourceEndpoint.GetPodName(),
		DestinationK8sMeta: flow.DestinationEndpoint.GetPodName(),
	}
}

func convertFalcoFlowToFlowKey(flow *pb.FalcoFlow) FlowKey {
	if flow == nil {
		return FlowKeyFalco{}
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

	return FlowKeyFalco{
		Ts:              timestamp,
		SourceIP:        srcIP,
		DestinationIP:   dstIP,
		SourcePort:      srcPort,
		DestinationPort: dstPort,
		Protocol:        proto,
	}
}
