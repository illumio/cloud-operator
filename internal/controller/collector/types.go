// Copyright 2024 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"errors"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// Protocol constants.
const (
	ICMP = "icmp"
	TCP  = "tcp"
	UDP  = "udp"
	SCTP = "sctp"

	IPv4 = "ipv4"
	IPv6 = "ipv6"
)

// Errors for Falco flow parsing.
var (
	ErrFalcoEventIsNotFlow   = errors.New("ignoring falco event, not a network flow")
	ErrFalcoIncompleteL3Flow = errors.New("ignoring incomplete falco l3 network flow")
	ErrFalcoIncompleteL4Flow = errors.New("ignoring incomplete falco l4 network flow")
	ErrFalcoInvalidPort      = errors.New("ignoring incomplete falco flow due to bad ports")
	ErrFalcoTimestamp        = errors.New("incomplete or incorrectly formatted timestamp found in Falco flow")
)

// FlowSink is the interface for caching network flows.
type FlowSink interface {
	CacheFlow(ctx context.Context, flow pb.Flow) error
	IncrementFlowsReceived()
}

// CreateLayer3Message creates a Layer3 IP message from source/destination addresses.
func CreateLayer3Message(source string, destination string, ipVersion string) (*pb.IP, error) {
	switch ipVersion {
	case IPv4:
		return &pb.IP{Source: source, Destination: destination, IpVersion: pb.IPVersion_IP_VERSION_IPV4}, nil
	case IPv6:
		return &pb.IP{Source: source, Destination: destination, IpVersion: pb.IPVersion_IP_VERSION_IPV6}, nil
	default:
		// If this is IPVersion_IP_VERSION_IP_NOT_USED_UNSPECIFIED we want to drop this packet.
		return nil, ErrFalcoIncompleteL3Flow
	}
}

// CreateLayer4Message converts event protocol and ports to a Layer4 proto message.
func CreateLayer4Message(proto string, srcPort, dstPort uint32, ipVersion string) (*pb.Layer4, error) {
	switch proto {
	case TCP:
		return &pb.Layer4{
			Protocol: &pb.Layer4_Tcp{
				Tcp: &pb.TCP{
					SourcePort:      srcPort,
					DestinationPort: dstPort,
					Flags:           &pb.TCPFlags{},
				},
			},
		}, nil
	case UDP:
		return &pb.Layer4{
			Protocol: &pb.Layer4_Udp{
				Udp: &pb.UDP{
					SourcePort:      srcPort,
					DestinationPort: dstPort,
				},
			},
		}, nil
	case ICMP:
		switch ipVersion {
		case IPv4:
			return &pb.Layer4{
				Protocol: &pb.Layer4_Icmpv4{
					Icmpv4: &pb.ICMPv4{},
				},
			}, nil
		case IPv6:
			return &pb.Layer4{
				Protocol: &pb.Layer4_Icmpv6{
					Icmpv6: &pb.ICMPv6{},
				},
			}, nil
		}
	default:
	}

	return nil, ErrFalcoIncompleteL4Flow
}
