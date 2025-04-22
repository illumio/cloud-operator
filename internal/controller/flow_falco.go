// Copyright 2024 Illumio, Inc. All Rights Reserved.
package controller

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// FalcoEvent represents the network information extracted from a Falco event.
type FalcoEvent struct {
	// Timestamp is the time the network event occured. ISO 8601 format
	Timestamp *timestamppb.Timestamp `json:"time"`
	// SrcIP is the source IP address involved in the network event.
	SrcIP string `json:"srcip"`
	// DstIP is the destination IP address involved in the network event.
	DstIP string `json:"dstip"`
	// SrcPort is the source port number involved in the network event.
	SrcPort string `json:"srcport"`
	// DstPort is the destination port number involved in the network event.
	DstPort string `json:"dstport"`
	// Proto is the protocol used in the network event (e.g., TCP, UDP).
	Proto string `json:"proto"`
	// IpVersion is the version used in the network event (e.g. ipv4, ipv6).
	IpVersion string `json:"prototype"`
}

type FlowKeyFalco struct {
	Ts                 int64
	SourceIP           string
	DestinationIP      string
	SourcePort         int
	DestinationPort    int
	Protocol           string
	SourceK8sMeta      string
	DestinationK8sMeta string
}

// Methods required by FlowKey interface
func (f FlowKeyFalco) Timestamp() int64            { return f.Ts }
func (f FlowKeyFalco) SrcIP() string               { return f.SourceIP }
func (f FlowKeyFalco) DstIP() string               { return f.DestinationIP }
func (f FlowKeyFalco) SrcPort() int                { return f.SourcePort }
func (f FlowKeyFalco) DstPort() int                { return f.DestinationPort }
func (f FlowKeyFalco) Proto() string               { return f.Protocol }
func (f FlowKeyFalco) SourceEndpoint() string      { return "" }
func (f FlowKeyFalco) DestinationEndpoint() string { return "" }

// falcoTimestampFormat  is the format of timestamp strings received from Falco
const falcoTimestampFormat = "2006-01-02T15:04:05.999999999-0700"

// removeTrailingTab removes the trailing tab character from the input string if it exists.
// Within the falco network logs, the timestamp comes with a trailing '\t', this function
// trims that tab off before sending up to CloudSecure.
func removeTrailingTab(time string) string {
	return strings.TrimRight(time, "\t")
}

// parseFalcoTimestamp  parses the input string value in a Falco's timestamp format that is similar to RFC3339 and returns a timestamppb.Timestamp.
func parseFalcoTimestamp(value string) (*timestamppb.Timestamp, error) {
	value = removeTrailingTab(value)

	// Parse the timestamp string using the specified layout
	t, err := time.Parse(falcoTimestampFormat, value)
	if err != nil {
		return nil, ErrFalcoTimestamp
	}

	return timestamppb.New(t), nil
}

// parsePodNetworkInfo parses the input string to extract network information into a FalcoFlow message.
func parsePodNetworkInfo(input string) (*pb.FalcoFlow, error) {
	var info FalcoEvent
	// Regular expression to extract the key-value pairs from the input string
	matches := reParsePodNetworkInfo.FindAllStringSubmatch(input, -1)

	for _, match := range matches {
		if len(match) == 3 {
			key, value := match[1], match[2]
			switch key {
			case "time":
				ts, err := parseFalcoTimestamp(value)
				if err != nil {
					return nil, err
				}
				info.Timestamp = ts
			case "srcip":
				info.SrcIP = value
			case "dstip":
				info.DstIP = value
			case "srcport":
				info.SrcPort = value
			case "dstport":
				info.DstPort = value
			case "proto":
				info.Proto = value
			case "ipversion":
				info.IpVersion = value
			}
		}
	}
	if (FalcoEvent{}) == info {
		return nil, ErrFalcoEventIsNotFlow
	}

	layer3Message, err := createLayer3Message(info.SrcIP, info.DstIP, info.IpVersion)
	if err != nil {
		return nil, err
	}

	srcPort, err := strconv.ParseUint(info.SrcPort, 10, 32)
	if err != nil {
		return nil, ErrFalcoInvalidPort
	}
	dstPort, err := strconv.ParseUint(info.DstPort, 10, 32)
	if err != nil {
		return nil, ErrFalcoInvalidPort
	}

	layer4Message, err := createLayer4Message(info.Proto, uint32(srcPort), uint32(dstPort), info.IpVersion)
	if err != nil {
		return nil, err
	}

	flow := &pb.FalcoFlow{
		Layer3: layer3Message,
		Layer4: layer4Message,
		Ts: &pb.FalcoFlow_Timestamp{
			Timestamp: info.Timestamp,
		},
	}

	return flow, nil

}

// NewFalcoEventHandler creates a new HTTP handler function for processing Falco events.
func NewFalcoEventHandler(eventChan chan<- string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Output string `json:"output"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		t := time.NewTimer(2 * time.Second)
		select {
		case eventChan <- body.Output:
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
			w.WriteHeader(http.StatusServiceUnavailable)
		case <-t.C:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		t.Stop()
	}
}

// filterIllumioTraffic filters out events related to Illumio network traffic.
func filterIllumioTraffic(body string) bool {
	return strings.Contains(body, "illumio_network_traffic")
}

func createLayer3Message(source string, destination string, ipVersion string) (*pb.IP, error) {
	if ipVersion == "ipv4" {
		return &pb.IP{Source: source, Destination: destination, IpVersion: pb.IPVersion_IP_VERSION_IPV4}, nil
	} else if ipVersion == "ipv6" {
		return &pb.IP{Source: source, Destination: destination, IpVersion: pb.IPVersion_IP_VERSION_IPV6}, nil
	}
	// If this is IPVersion_IP_VERSION_IP_NOT_USED_UNSPECIFIED we want to drop this packet.
	return nil, ErrFalcoIncompleteL3Flow
}

// createLayer4Message converts event protocol and ports to a Layer4 proto message
func createLayer4Message(proto string, srcPort, dstPort uint32, ipVersion string) (*pb.Layer4, error) {
	switch proto {
	case "tcp":
		return &pb.Layer4{
			Protocol: &pb.Layer4_Tcp{
				Tcp: &pb.TCP{
					SourcePort:      srcPort,
					DestinationPort: dstPort,
					Flags:           &pb.TCPFlags{},
				},
			},
		}, nil
	case "udp":
		return &pb.Layer4{
			Protocol: &pb.Layer4_Udp{
				Udp: &pb.UDP{
					SourcePort:      srcPort,
					DestinationPort: dstPort,
				},
			},
		}, nil
	case "icmp":
		if ipVersion == "ipv4" {
			return &pb.Layer4{
				Protocol: &pb.Layer4_Icmpv4{
					Icmpv4: &pb.ICMPv4{},
				},
			}, nil
		} else if ipVersion == "ipv6" {
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
