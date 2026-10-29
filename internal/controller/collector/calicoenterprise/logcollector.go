// Copyright 2026 Illumio, Inc. All Rights Reserved.

package calicoenterprise

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/collector"
)

// Errors for Calico Enterprise LogCollector record parsing.
var (
	ErrNotLogCollectorRecord = errors.New("not a calico LogCollector record")
	ErrMalformedRecord       = errors.New("ignoring calico LogCollector record with malformed JSON")
	ErrAggregatedFlow        = errors.New("ignoring aggregated calico flow without source or destination IP addresses and ports")
	ErrMissingTimestamp      = errors.New("ignoring calico flow without a start time")
)

// calicoLogCollectorAppName is the syslog app name of every flow record that
// Calico Enterprise's LogCollector forwards to a syslog store. Fluentd
// (Tigera operator v1.43 and earlier, up to Calico Enterprise 3.24 EP1) sends
// RFC 3164 records, and Fluent Bit (operator v1.44 and later, Calico Enterprise
// 3.24 EP3 and later) sends RFC 5424 records with empty procid, msgid and
// structured data, and a UTF-8 BOM before the message:
//
//	<14>Oct 26 21:10:09 calico-ent tigera_secure: {"source_ip":"10.244.163.79",...}
//	<14>1 2026-10-26T21:10:09.000000Z calico-ent tigera_secure - - - \xef\xbb\xbf{"source_ip":"10.244.163.79",...}
const calicoLogCollectorAppName = " tigera_secure"

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
// Enterprise's LogCollector syslog store into a Flow.
func ParseLogCollectorFlow(line string) (Flow, error) {
	body, found := logCollectorMessage(line)
	if !found {
		return Flow{}, ErrNotLogCollectorRecord
	}

	var record calicoLogCollectorRecord
	if err := json.Unmarshal([]byte(body), &record); err != nil {
		return Flow{}, fmt.Errorf("%w: %w", ErrMalformedRecord, err)
	}

	if record.SourceIP == "" || record.DestIP == "" || record.Proto == "" {
		return Flow{}, ErrIncompleteFlow
	}

	protocol, err := parseLogCollectorProtocol(record.Proto)
	if err != nil {
		return Flow{}, err
	}

	// Calico aggregates flow logs by default: aggregated records replace IP
	// addresses with "-" and ports with null. ICMP has no ports.
	if record.SourceIP == calicoAggregatedValue || record.DestIP == calicoAggregatedValue {
		return Flow{}, ErrAggregatedFlow
	}

	var sourcePort, destPort uint32

	if protocol != collector.ICMP {
		if record.SourcePort == nil || record.DestPort == nil {
			return Flow{}, ErrAggregatedFlow
		}

		sourcePort, destPort = *record.SourcePort, *record.DestPort
	}

	if sourcePort > 65535 || destPort > 65535 {
		return Flow{}, ErrInvalidPort
	}

	ipVersion, err := calicoIPVersion(record.SourceIP)
	if err != nil {
		return Flow{}, err
	}

	if _, err := calicoIPVersion(record.DestIP); err != nil {
		return Flow{}, err
	}

	flow := Flow{
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
		return Flow{}, ErrMissingTimestamp
	}

	flow.StartTimestamp = timestamppb.New(time.Unix(record.StartTime, 0))

	return flow, nil
}

// ParseFlow parses a single flow record forwarded by Calico
// Enterprise's LogCollector syslog store into the five-tuple flow sent to
// CloudSecure.
func ParseFlow(line string) (*pb.FiveTupleFlow, error) {
	flow, err := ParseLogCollectorFlow(line)
	if err != nil {
		return nil, err
	}

	layer3, err := collector.CreateLayer3Message(flow.SourceIP, flow.DestinationIP, flow.IPVersion)
	if err != nil {
		return nil, err
	}

	layer4, err := collector.CreateLayer4Message(flow.Protocol, uint32(flow.SourcePort), uint32(flow.DestinationPort), flow.IPVersion)
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

// logCollectorMessage returns the JSON message of a LogCollector syslog record
// in either syslog format: the message starts at the first "{" after the app
// name, which skips the RFC 3164 ":" or the RFC 5424 header fields and BOM.
func logCollectorMessage(line string) (string, bool) {
	jsonStart := strings.IndexByte(line, '{')
	if jsonStart < 0 {
		return "", false
	}

	appName := strings.Index(line[:jsonStart], calicoLogCollectorAppName)
	if appName < 0 {
		return "", false
	}

	return line[jsonStart:], true
}

// parseLogCollectorProtocol maps the protocol name in a LogCollector record to
// the protocol name expected by CreateLayer4Message.
func parseLogCollectorProtocol(value string) (string, error) {
	switch strings.ToLower(value) {
	case collector.ICMP:
		return collector.ICMP, nil
	case collector.TCP:
		return collector.TCP, nil
	case collector.UDP:
		return collector.UDP, nil
	case collector.SCTP:
		return collector.SCTP, nil
	default:
		return "", ErrUnknownProto
	}
}
