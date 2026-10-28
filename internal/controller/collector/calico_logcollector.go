// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// Errors for Calico Enterprise LogCollector record parsing.
var (
	ErrCalicoNotLogCollectorRecord = errors.New("not a calico LogCollector record")
	ErrCalicoMalformedRecord       = errors.New("ignoring calico LogCollector record with malformed JSON")
	ErrCalicoAggregatedFlow        = errors.New("ignoring aggregated calico flow without source or destination IP addresses and ports")
	ErrCalicoMissingTimestamp      = errors.New("ignoring calico flow without a start time")
)

// calicoLogCollectorTag precedes the JSON body of every flow record that Calico
// Enterprise's LogCollector forwards to a syslog store:
//
//	<14>Oct 26 21:10:09 calico-ent tigera_secure: {"source_ip":"10.244.163.79",...}
const calicoLogCollectorTag = "tigera_secure: "

// calicoAggregatedValue replaces the IP addresses of aggregated flow records.
const calicoAggregatedValue = "-"

// calicoLogCollectorRecord holds the five-tuple fields of a LogCollector flow
// record. Ports are pointers so that a null or missing port is distinguishable
// from port 0.
type calicoLogCollectorRecord struct {
	StartTime  int64   `json:"start_time"`
	SourceIP   string  `json:"source_ip"`
	SourcePort *uint32 `json:"source_port"`
	DestIP     string  `json:"dest_ip"`
	DestPort   *uint32 `json:"dest_port"`
	Proto      string  `json:"proto"`
}

// ParseLogCollectorFlow parses a single flow record forwarded by Calico
// Enterprise's LogCollector syslog store into a CalicoFlow.
func ParseLogCollectorFlow(line string) (CalicoFlow, error) {
	_, body, found := strings.Cut(line, calicoLogCollectorTag)
	if !found {
		return CalicoFlow{}, ErrCalicoNotLogCollectorRecord
	}

	var record calicoLogCollectorRecord
	if err := json.Unmarshal([]byte(body), &record); err != nil {
		return CalicoFlow{}, fmt.Errorf("%w: %w", ErrCalicoMalformedRecord, err)
	}

	if record.SourceIP == "" || record.DestIP == "" || record.Proto == "" {
		return CalicoFlow{}, ErrCalicoIncompleteFlow
	}

	protocol, err := parseLogCollectorProtocol(record.Proto)
	if err != nil {
		return CalicoFlow{}, err
	}

	// Calico aggregates flow logs by default: aggregated records replace IP
	// addresses with "-" and ports with null. ICMP has no ports.
	if record.SourceIP == calicoAggregatedValue || record.DestIP == calicoAggregatedValue {
		return CalicoFlow{}, ErrCalicoAggregatedFlow
	}

	var sourcePort, destPort uint32

	if protocol != ICMP {
		if record.SourcePort == nil || record.DestPort == nil {
			return CalicoFlow{}, ErrCalicoAggregatedFlow
		}

		sourcePort, destPort = *record.SourcePort, *record.DestPort
	}

	if sourcePort > 65535 || destPort > 65535 {
		return CalicoFlow{}, ErrCalicoInvalidPort
	}

	ipVersion, err := calicoIPVersion(record.SourceIP)
	if err != nil {
		return CalicoFlow{}, err
	}

	if _, err := calicoIPVersion(record.DestIP); err != nil {
		return CalicoFlow{}, err
	}

	flow := CalicoFlow{
		SourceIP:        record.SourceIP,
		DestinationIP:   record.DestIP,
		SourcePort:      uint16(sourcePort),
		DestinationPort: uint16(destPort),
		Protocol:        protocol,
		IPVersion:       ipVersion,
	}

	// Drop flows without a start time, like the other collectors: an empty
	// timestamp would date the flow 1970.
	if record.StartTime <= 0 {
		return CalicoFlow{}, ErrCalicoMissingTimestamp
	}

	flow.StartTimestamp = timestamppb.New(time.Unix(record.StartTime, 0))

	return flow, nil
}

// ParseCalicoEnterpriseFlow parses a single flow record forwarded by Calico
// Enterprise's LogCollector syslog store into the five-tuple flow sent to
// CloudSecure.
func ParseCalicoEnterpriseFlow(line string) (*pb.FiveTupleFlow, error) {
	flow, err := ParseLogCollectorFlow(line)
	if err != nil {
		return nil, err
	}

	layer3, err := CreateLayer3Message(flow.SourceIP, flow.DestinationIP, flow.IPVersion)
	if err != nil {
		return nil, err
	}

	layer4, err := CreateLayer4Message(flow.Protocol, uint32(flow.SourcePort), uint32(flow.DestinationPort), flow.IPVersion)
	if err != nil {
		return nil, err
	}

	return &pb.FiveTupleFlow{
		Layer3: layer3,
		Layer4: layer4,
		Ts: &pb.FiveTupleFlow_Timestamp{
			Timestamp: flow.StartTimestamp,
		},
	}, nil
}

// parseLogCollectorProtocol maps the protocol name in a LogCollector record to
// the protocol name expected by CreateLayer4Message.
func parseLogCollectorProtocol(value string) (string, error) {
	switch strings.ToLower(value) {
	case ICMP:
		return ICMP, nil
	case TCP:
		return TCP, nil
	case UDP:
		return UDP, nil
	case SCTP:
		return SCTP, nil
	default:
		return "", ErrCalicoUnknownProto
	}
}
