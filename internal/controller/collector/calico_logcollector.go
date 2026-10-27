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
)

// calicoLogCollectorTag precedes the JSON body of every flow record that Calico
// Enterprise's LogCollector forwards to a syslog store:
//
//	<14>Oct 26 21:10:09 calico-ent tigera_secure: {"source_ip":"10.244.163.79",...}
const calicoLogCollectorTag = "tigera_secure: "

// calicoLogCollectorRecord holds the fields of a LogCollector flow record that
// are sent to CloudSecure. Ports are pointers so that a null or missing port
// (aggregated flows) is distinguishable from port 0 (ICMP).
type calicoLogCollectorRecord struct {
	StartTime  int64   `json:"start_time"`
	SourceIP   string  `json:"source_ip"`
	SourcePort *uint32 `json:"source_port"`
	DestIP     string  `json:"dest_ip"`
	DestPort   *uint32 `json:"dest_port"`
	Proto      string  `json:"proto"`
	Action     string  `json:"action"`
	Reporter   string  `json:"reporter"`
	Policies   struct {
		Enforced []string `json:"enforced_policies"`
	} `json:"policies"`
}

// calicoPolicyKinds maps the kind prefix of a Calico policy string to the
// kind of the policy resource.
var calicoPolicyKinds = map[string]string{
	"np":    "NetworkPolicy",
	"gnp":   "GlobalNetworkPolicy",
	"snp":   "StagedNetworkPolicy",
	"sgnp":  "StagedGlobalNetworkPolicy",
	"knp":   "KubernetesNetworkPolicy",
	"sknp":  "StagedKubernetesNetworkPolicy",
	"kanp":  "AdminNetworkPolicy",
	"kbanp": "BaselineAdminNetworkPolicy",
}

// ParseLogCollectorFlow parses a single flow record forwarded by Calico
// Enterprise's LogCollector syslog store into a CalicoFlow.
func ParseLogCollectorFlow(line string) (CalicoFlow, error) {
	_, flow, err := parseLogCollectorRecord(line)

	return flow, err
}

// ParseCalicoEnterpriseFlow parses a single flow record forwarded by Calico
// Enterprise's LogCollector syslog store into the five-tuple flow sent to
// CloudSecure.
//
// The reporter gives the traffic direction (src: EGRESS, dst: INGRESS) and the
// action gives the verdict. Each enforced policy is listed as allowing or
// denying the flow, on the side that reported it, according to the policy's
// own action. Unknown reporters and actions are sent as unspecified.
func ParseCalicoEnterpriseFlow(line string) (*pb.FiveTupleFlow, error) {
	record, flow, err := parseLogCollectorRecord(line)
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

	enterpriseFlow := &pb.FiveTupleFlow{
		Layer3: layer3,
		Layer4: layer4,
		Ts: &pb.FiveTupleFlow_Timestamp{
			Timestamp: flow.StartTimestamp,
		},
	}

	switch record.Action {
	case "allow":
		enterpriseFlow.Verdict = pb.Verdict_VERDICT_FORWARDED
	case "deny":
		enterpriseFlow.Verdict = pb.Verdict_VERDICT_DROPPED
	}

	var allowed, denied *[]*pb.Policy

	switch record.Reporter {
	case "src":
		enterpriseFlow.TrafficDirection = pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS
		allowed, denied = &enterpriseFlow.EgressAllowedBy, &enterpriseFlow.EgressDeniedBy
	case "dst":
		enterpriseFlow.TrafficDirection = pb.TrafficDirection_TRAFFIC_DIRECTION_INGRESS
		allowed, denied = &enterpriseFlow.IngressAllowedBy, &enterpriseFlow.IngressDeniedBy
	default:
		// Without a reporting side, policies cannot be attributed to a direction.
		return enterpriseFlow, nil
	}

	for _, value := range record.Policies.Enforced {
		policy, action, ok := parseCalicoPolicy(value)
		if !ok {
			continue
		}

		switch action {
		case "allow":
			*allowed = append(*allowed, policy)
		case "deny":
			*denied = append(*denied, policy)
		}
	}

	return enterpriseFlow, nil
}

// parseCalicoPolicy parses a Calico Enterprise policy string such as
//
//	0|calico-system|np:kube-system/calico-system.cluster-dns|allow|0
//
// (tier index | tier | kind:namespace/name | action | rule index) into a policy
// and its action. It reports false for strings it cannot parse.
func parseCalicoPolicy(value string) (*pb.Policy, string, bool) {
	fields := strings.Split(value, "|")
	if len(fields) != 5 {
		return nil, "", false
	}

	policy := &pb.Policy{}

	reference := fields[2]
	if kind, rest, found := strings.Cut(reference, ":"); found {
		policy.Kind = kind
		if fullKind, known := calicoPolicyKinds[kind]; known {
			policy.Kind = fullKind
		}

		reference = rest
	}

	if namespace, name, found := strings.Cut(reference, "/"); found {
		policy.Namespace = namespace
		reference = name
	}

	if reference == "" {
		return nil, "", false
	}

	policy.Name = reference

	return policy, fields[3], true
}

// parseLogCollectorRecord decodes a LogCollector flow record and validates its
// five-tuple.
func parseLogCollectorRecord(line string) (calicoLogCollectorRecord, CalicoFlow, error) {
	_, body, found := strings.Cut(line, calicoLogCollectorTag)
	if !found {
		return calicoLogCollectorRecord{}, CalicoFlow{}, ErrCalicoNotLogCollectorRecord
	}

	var record calicoLogCollectorRecord
	if err := json.Unmarshal([]byte(body), &record); err != nil {
		return calicoLogCollectorRecord{}, CalicoFlow{}, fmt.Errorf("%w: %w", ErrCalicoMalformedRecord, err)
	}

	if record.SourceIP == "" || record.DestIP == "" || record.SourcePort == nil || record.DestPort == nil || record.Proto == "" {
		return calicoLogCollectorRecord{}, CalicoFlow{}, ErrCalicoIncompleteFlow
	}

	if *record.SourcePort > 65535 || *record.DestPort > 65535 {
		return calicoLogCollectorRecord{}, CalicoFlow{}, ErrCalicoInvalidPort
	}

	protocol, err := parseLogCollectorProtocol(record.Proto)
	if err != nil {
		return calicoLogCollectorRecord{}, CalicoFlow{}, err
	}

	ipVersion, err := calicoIPVersion(record.SourceIP)
	if err != nil {
		return calicoLogCollectorRecord{}, CalicoFlow{}, err
	}

	if _, err := calicoIPVersion(record.DestIP); err != nil {
		return calicoLogCollectorRecord{}, CalicoFlow{}, err
	}

	flow := CalicoFlow{
		SourceIP:        record.SourceIP,
		DestinationIP:   record.DestIP,
		SourcePort:      uint16(*record.SourcePort),
		DestinationPort: uint16(*record.DestPort),
		Protocol:        protocol,
		IPVersion:       ipVersion,
	}

	if record.StartTime > 0 {
		flow.StartTimestamp = timestamppb.New(time.Unix(record.StartTime, 0))
	}

	return record, flow, nil
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
