// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// VPC CNI flow log errors.
var (
	ErrVPCCNIInvalidLog = errors.New("invalid VPC CNI flow log format")
	ErrVPCCNIInvalidIP  = errors.New("invalid IP address in VPC CNI flow log")
	ErrVPCCNINotFlowLog = errors.New("log line is not a flow log")
)

// VPCCNIFlowLog represents the flow log format from aws-eks-nodeagent.
// Actual format from AWS VPC CNI Network Policy Agent:
// {"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client",
//
//	"msg":"Flow Info: ","Src IP":"10.0.141.167","Src Port":39197,
//	"Dest IP":"172.20.0.10","Dest Port":53,"Proto":"TCP","Verdict":"ACCEPT"}
type VPCCNIFlowLog struct {
	Level     string `json:"level"`
	Timestamp string `json:"ts"`
	Logger    string `json:"logger"`
	Message   string `json:"msg"`
	SrcIP     string `json:"Src IP"`
	SrcPort   uint32 `json:"Src Port"`
	DestIP    string `json:"Dest IP"`
	DestPort  uint32 `json:"Dest Port"`
	Proto     string `json:"Proto"`   // TCP, UDP, ICMP, SCTP, UNKNOWN
	Verdict   string `json:"Verdict"` // ACCEPT, DENY, EXPIRED/DELETED
}

// ParseVPCCNIFlowLog parses a VPC CNI flow log line into a FiveTupleFlow.
func ParseVPCCNIFlowLog(line string) (*pb.FiveTupleFlow, error) {
	var log VPCCNIFlowLog

	if err := json.Unmarshal([]byte(line), &log); err != nil {
		return nil, ErrVPCCNINotFlowLog
	}

	// Check if this is a flow log (must have "Flow Info" message and ebpf-client logger)
	if !strings.Contains(log.Message, "Flow Info") || log.Logger != "ebpf-client" {
		return nil, ErrVPCCNINotFlowLog
	}

	// Validate required fields
	if log.SrcIP == "" || log.DestIP == "" {
		return nil, ErrVPCCNIInvalidLog
	}

	// Determine IP version
	ipVersion := "ipv4"
	if isIPv6(log.SrcIP) || isIPv6(log.DestIP) {
		ipVersion = "ipv6"
	}

	layer3Message, err := CreateLayer3Message(log.SrcIP, log.DestIP, ipVersion)
	if err != nil {
		return nil, ErrVPCCNIInvalidIP
	}

	// Convert protocol string to lowercase for CreateLayer4Message
	protoStr := strings.ToLower(log.Proto)
	if protoStr == "unknown" {
		protoStr = "tcp" // default to TCP for unknown
	}

	layer4Message, err := CreateLayer4Message(protoStr, log.SrcPort, log.DestPort, ipVersion)
	if err != nil {
		return nil, err
	}

	// Parse timestamp or use current time
	ts := timestamppb.Now()
	if log.Timestamp != "" {
		// AWS uses ISO 8601 format: "2024-09-23T12:36:53.562Z"
		if parsedTime, err := time.Parse(time.RFC3339Nano, log.Timestamp); err == nil {
			ts = timestamppb.New(parsedTime)
		} else if parsedTime, err := time.Parse("2006-01-02T15:04:05.999Z", log.Timestamp); err == nil {
			ts = timestamppb.New(parsedTime)
		}
	}

	flow := &pb.FiveTupleFlow{
		Layer3: layer3Message,
		Layer4: layer4Message,
		Ts: &pb.FiveTupleFlow_Timestamp{
			Timestamp: ts,
		},
	}

	return flow, nil
}

// isIPv6 checks if the given address is an IPv6 address.
func isIPv6(addr string) bool {
	for _, c := range addr {
		if c == ':' {
			return true
		}
	}

	return false
}
