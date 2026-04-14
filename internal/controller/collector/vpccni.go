// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

const (
	// AWSNodeLabel is the label selector for aws-node pods.
	AWSNodeLabel = "k8s-app=aws-node"
	// AWSNodeNamespace is the namespace where aws-node pods run.
	AWSNodeNamespace = "kube-system"
	// AWSEksNodeagentContainer is the container name that has flow logs.
	AWSEksNodeagentContainer = "aws-eks-nodeagent"
)

// VPC CNI flow log errors.
var (
	ErrVPCCNIInvalidLog = errors.New("invalid VPC CNI flow log format")
	ErrVPCCNIInvalidIP  = errors.New("invalid IP address in VPC CNI flow log")
	ErrVPCCNINotFlowLog = errors.New("log line is not a flow log")
)

// VPCCNIFlowLog represents the flow log format from aws-eks-nodeagent.
//
// Old format (v1.0.x - v1.2.1):
// {"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client",
//
//	"msg":"Flow Info: ","Src IP":"10.0.141.167","Src Port":39197,
//	"Dest IP":"172.20.0.10","Dest Port":53,"Proto":"TCP","Verdict":"ACCEPT"}
//
// New format (v1.2.2+):
// {"level":"debug","ts":"2026-04-13T21:18:46.888Z","caller":"runtime/asm_amd64.s:1700",
//
//	"msg":"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction egress"}
type VPCCNIFlowLog struct {
	Level     string `json:"level"`
	Timestamp string `json:"ts"`
	Logger    string `json:"logger"` // v1.0.x - v1.2.1
	Caller    string `json:"caller"` // v1.2.2+
	Message   string `json:"msg"`
	SrcIP     string `json:"Src IP"`
	SrcPort   uint32 `json:"Src Port"`
	DestIP    string `json:"Dest IP"`
	DestPort  uint32 `json:"Dest Port"`
	Proto     string `json:"Proto"`   // TCP, UDP, ICMP, SCTP, UNKNOWN
	Verdict   string `json:"Verdict"` // ACCEPT, DENY, EXPIRED/DELETED
}

// flowMsgPattern extracts flow data from the embedded msg string in v1.2.2+ format.
// Example: "Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction egress".
var flowMsgPattern = regexp.MustCompile(
	`Src IP:\s*(\S+)\s+Src Port:\s*(\d+)\s+Dest IP:\s*(\S+)\s+Dest Port:\s*(\d+)\s+Proto\s+(\S+)\s+Verdict\s+(\S+)`,
)

// parseFlowFromMsg extracts flow data from the embedded msg string (v1.2.2+ format).
func parseFlowFromMsg(msg string) (srcIP string, srcPort uint32, destIP string, destPort uint32, proto string, verdict string, ok bool) {
	matches := flowMsgPattern.FindStringSubmatch(msg)
	if len(matches) < 7 {
		return "", 0, "", 0, "", "", false
	}

	srcPortInt, err := strconv.ParseUint(matches[2], 10, 32)
	if err != nil {
		return "", 0, "", 0, "", "", false
	}

	destPortInt, err := strconv.ParseUint(matches[4], 10, 32)
	if err != nil {
		return "", 0, "", 0, "", "", false
	}

	return matches[1], uint32(srcPortInt), matches[3], uint32(destPortInt), matches[5], matches[6], true
}

// ParseVPCCNIFlowLog parses a VPC CNI flow log line into a FiveTupleFlow.
// Supports both old format (v1.0.x - v1.2.1) with separate JSON fields
// and new format (v1.2.2+) with embedded msg string.
func ParseVPCCNIFlowLog(line string) (*pb.FiveTupleFlow, error) {
	var log VPCCNIFlowLog

	if err := json.Unmarshal([]byte(line), &log); err != nil {
		return nil, ErrVPCCNINotFlowLog
	}

	// Check if this is a flow log (must have "Flow Info" in message)
	if !strings.Contains(log.Message, "Flow Info") {
		return nil, ErrVPCCNINotFlowLog
	}

	// For old format (v1.0.x - v1.2.1), also require ebpf-client logger
	// For new format (v1.2.2+), the logger field is empty and caller is set
	isOldFormat := log.Logger == "ebpf-client"
	isNewFormat := log.Caller != "" && log.Logger == ""

	if !isOldFormat && !isNewFormat {
		return nil, ErrVPCCNINotFlowLog
	}

	var (
		srcIP, destIP, proto string
		srcPort, destPort    uint32
	)

	if isOldFormat && log.SrcIP != "" && log.DestIP != "" {
		// Old format: fields are in separate JSON keys
		srcIP = log.SrcIP
		srcPort = log.SrcPort
		destIP = log.DestIP
		destPort = log.DestPort
		proto = log.Proto
	} else {
		// New format (v1.2.2+): parse from embedded msg string
		var ok bool

		srcIP, srcPort, destIP, destPort, proto, _, ok = parseFlowFromMsg(log.Message)
		if !ok {
			return nil, ErrVPCCNIInvalidLog
		}
	}

	// Validate required fields
	if srcIP == "" || destIP == "" {
		return nil, ErrVPCCNIInvalidLog
	}

	// Determine IP version
	ipVersion := "ipv4"
	if isIPv6(srcIP) || isIPv6(destIP) {
		ipVersion = "ipv6"
	}

	layer3Message, err := CreateLayer3Message(srcIP, destIP, ipVersion)
	if err != nil {
		return nil, ErrVPCCNIInvalidIP
	}

	// Convert protocol string to lowercase for CreateLayer4Message
	protoStr := strings.ToLower(proto)
	if protoStr == "unknown" {
		protoStr = "tcp" // default to TCP for unknown
	}

	layer4Message, err := CreateLayer4Message(protoStr, srcPort, destPort, ipVersion)
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

// IsVPCCNIAvailable checks if AWS VPC CNI with flow logging is available in the cluster.
// It looks for aws-node pods with the aws-eks-nodeagent container.
func IsVPCCNIAvailable(ctx context.Context, logger *zap.Logger, k8sClient kubernetes.Interface) bool {
	pods, err := k8sClient.CoreV1().Pods(AWSNodeNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: AWSNodeLabel,
		Limit:         1,
	})
	if err != nil {
		logger.Debug("Failed to list aws-node pods", zap.Error(err))

		return false
	}

	if len(pods.Items) == 0 {
		logger.Debug("No aws-node pods found")

		return false
	}

	// Check if the aws-eks-nodeagent container exists
	if hasNodeagentContainer(pods.Items[0]) {
		logger.Debug("VPC CNI with aws-eks-nodeagent detected")

		return true
	}

	logger.Debug("aws-node pods found but aws-eks-nodeagent container not present")

	return false
}

// hasNodeagentContainer checks if the pod has the aws-eks-nodeagent container.
func hasNodeagentContainer(pod corev1.Pod) bool {
	for _, container := range pod.Spec.Containers {
		if container.Name == AWSEksNodeagentContainer {
			return true
		}
	}

	return false
}
