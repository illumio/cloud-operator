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
	// AWSNodeNamespace is the namespace of the aws-node pods.
	AWSNodeNamespace = "kube-system"
	// AWSEksNodeagentContainer is the name of the container that has flow logs.
	AWSEksNodeagentContainer = "aws-eks-nodeagent"

	// MaxFlowAge bounds how far in the past an AWS VPC CNI flow may be and still be
	// worth sending. Both collectors re-read a pod/daemon's whole agent log on their
	// first scrape after a restart (the read position is not persisted), so the log
	// can contain flows far older than the backend's stream time window. The backend
	// discards flows outside that window anyway, so CacheFlowLine filters any flow
	// whose timestamp is older than MaxFlowAge before it is cached and sent.
	MaxFlowAge = 1 * time.Hour
)

// AWS VPC CNI flow log errors.
var (
	ErrAWSVPCCNIInvalidLog       = errors.New("invalid AWS VPC CNI flow log format")
	ErrAWSVPCCNIInvalidIP        = errors.New("invalid IP address in AWS VPC CNI flow log")
	ErrAWSVPCCNINotFlowLog       = errors.New("log line is not a flow log")
	ErrAWSVPCCNIInvalidProtocol  = errors.New("unsupported protocol in AWS VPC CNI flow log")
	ErrAWSVPCCNIInvalidTimestamp = errors.New("invalid or missing timestamp in AWS VPC CNI flow log")
)

// AWSVPCCNIFlowLog represents the flow log format from aws-eks-nodeagent.
//
// Old format (v1.0.x - v1.2.1):
//
//	{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client",
//	 "msg":"Flow Info: ","Src IP":"10.0.141.167","Src Port":39197,
//	 "Dest IP":"172.20.0.10","Dest Port":53,"Proto":"TCP","Verdict":"ACCEPT"}
//
// New format (v1.2.2+):
//
//	{"level":"debug","ts":"2026-04-13T21:18:46.888Z","caller":"runtime/asm_amd64.s:1700",
//	 "msg":"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction egress"}
type AWSVPCCNIFlowLog struct {
	Level     string `json:"level"`
	Timestamp string `json:"ts"`
	Logger    string `json:"logger"` // v1.0.x - v1.2.1
	Caller    string `json:"caller"` // v1.2.2+
	Message   string `json:"msg"`
	SrcIP     string `json:"Src IP"`

	SrcPort  uint32 `json:"Src Port"`
	DestIP   string `json:"Dest IP"`
	DestPort uint32 `json:"Dest Port"`
	Proto    string `json:"Proto"`   // TCP, UDP, ICMP, SCTP, UNKNOWN
	Verdict  string `json:"Verdict"` // ACCEPT, DENY, EXPIRED/DELETED
}

// flowMsgPattern extracts flow data from the embedded msg string in v1.2.2+ format.
// Example: "Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction egress".
var flowMsgPattern = regexp.MustCompile(
	`Flow Info:\s*Src IP:\s*(\S+)\s+Src Port:\s*(\d+)\s+Dest IP:\s*(\S+)\s+Dest Port:\s*(\d+)\s+Proto\s+(\S+)\s+Verdict\s+(\S+)`,
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

// parseOldFormat extracts flow data from separate JSON fields (v1.0.x - v1.2.1 format).
func parseOldFormat(log *AWSVPCCNIFlowLog) (srcIP string, srcPort uint32, destIP string, destPort uint32, proto string, ok bool) {
	if log.SrcIP == "" || log.DestIP == "" {
		return "", 0, "", 0, "", false
	}

	return log.SrcIP, log.SrcPort, log.DestIP, log.DestPort, log.Proto, true
}

// ParseAWSVPCCNIFlowLog parses a VPC CNI flow log line into a FiveTupleFlow.
// Supports both old format (v1.0.x - v1.2.1) with separate JSON fields
// and new format (v1.2.2+) with embedded msg string.
func ParseAWSVPCCNIFlowLog(line string) (*pb.FiveTupleFlow, error) {
	var log AWSVPCCNIFlowLog

	if err := json.Unmarshal([]byte(line), &log); err != nil {
		return nil, ErrAWSVPCCNINotFlowLog
	}

	// Check if this is a flow log (must have "Flow Info" in message)
	if !strings.Contains(log.Message, "Flow Info") {
		return nil, ErrAWSVPCCNINotFlowLog
	}

	// For old format (v1.0.x - v1.2.1), also require ebpf-client logger
	// For new format (v1.2.2+), the logger field is empty and caller is set
	isOldFormat := log.Logger == "ebpf-client"
	isNewFormat := log.Caller != "" && log.Logger == ""

	if !isOldFormat && !isNewFormat {
		return nil, ErrAWSVPCCNINotFlowLog
	}

	var (
		srcIP, destIP, proto string
		srcPort, destPort    uint32
		ok                   bool
	)

	switch {
	case isOldFormat:
		srcIP, srcPort, destIP, destPort, proto, ok = parseOldFormat(&log)
	case isNewFormat:
		srcIP, srcPort, destIP, destPort, proto, _, ok = parseFlowFromMsg(log.Message)
	default:
		return nil, ErrAWSVPCCNINotFlowLog
	}

	if !ok {
		return nil, ErrAWSVPCCNIInvalidLog
	}

	// Determine IP version
	ipVersion := "ipv4"
	if isIPv6(srcIP) || isIPv6(destIP) {
		ipVersion = "ipv6"
	}

	layer3Message, err := CreateLayer3Message(srcIP, destIP, ipVersion)
	if err != nil {
		return nil, ErrAWSVPCCNIInvalidIP
	}

	// Convert protocol string to lowercase for CreateLayer4Message.
	// VPC CNI may report "UNKNOWN" for some protocols; default to TCP
	// as it's the most common and provides a reasonable flow record.
	protoStr := strings.ToLower(proto)
	if protoStr == "unknown" {
		protoStr = "tcp"
	}

	layer4Message, err := CreateLayer4Message(protoStr, srcPort, destPort, ipVersion)
	if err != nil {
		return nil, ErrAWSVPCCNIInvalidProtocol
	}

	// Parse timestamp - drop flows without valid timestamps (consistent with Cilium/Falco)
	if log.Timestamp == "" {
		return nil, ErrAWSVPCCNIInvalidTimestamp
	}

	var ts *timestamppb.Timestamp
	// AWS uses ISO 8601 format: "2024-09-23T12:36:53.562Z"
	if parsedTime, err := time.Parse(time.RFC3339Nano, log.Timestamp); err == nil {
		ts = timestamppb.New(parsedTime)
	} else if parsedTime, err := time.Parse("2006-01-02T15:04:05.999Z", log.Timestamp); err == nil {
		ts = timestamppb.New(parsedTime)
	} else {
		return nil, ErrAWSVPCCNIInvalidTimestamp
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

// CacheFlowLine is the shared parse -> stale-filter -> cache path for the AWS VPC
// CNI Network Policy Agent flow log format. It is used by BOTH the standard
// aws-node pod-log collector and the EKS Auto Mode node-proxy log collector so the
// two share identical flow handling.
//
// notBefore drops flows that fall outside the backend's stream time window.
// Neither collector persists its read position across restarts, so the first
// scrape of a pod/daemon re-reads the whole (up to ~200 MiB) active log; many of
// those records are far in the past. The backend discards flows outside its time
// window anyway, so sending them wastes the whole path from the operator onward.
// Dropping any flow whose log timestamp is before notBefore avoids that. Callers
// pass a ROLLING bound (typically time.Now().Add(-MaxFlowAge)) recomputed each
// poll, not a fixed startup time. A zero notBefore disables the filter (all flows
// pass).
//
// Returns cached=true when a flow was cached. A parse failure is not an error: the
// agent log interleaves non-flow housekeeping lines with flow records, so only a
// context error is returned, letting the caller stop without advancing its
// checkpoint. A CacheFlow error is logged and skipped (best-effort delivery).
func CacheFlowLine(ctx context.Context, sink FlowSink, line string, notBefore time.Time, logger *zap.Logger) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	flow, err := ParseAWSVPCCNIFlowLog(line)
	if err != nil {
		// Non-flow line: expected, not an error. See the return-value contract above.
		return false, nil //nolint:nilerr // non-flow lines are intentionally skipped, not errors
	}

	if !notBefore.IsZero() && flow.StartTimestamp().Before(notBefore) {
		// Flow predates the stream time window; drop it rather than sending a stale
		// record the backend would discard.
		return false, nil
	}

	if err := sink.CacheFlow(ctx, flow); err != nil {
		logger.Debug("failed to cache AWS VPC CNI flow", zap.Error(err))

		return false, nil
	}

	sink.IncrementFlowsReceived()

	return true, nil
}

// isIPv6 checks if the given address is an IPv6 address.
func isIPv6(addr string) bool {
	return strings.ContainsRune(addr, ':')
}

// IsAWSVPCCNIAvailable checks if AWS VPC CNI with flow logging is available in the cluster.
// It looks for aws-node pods with the aws-eks-nodeagent container.
// Checks multiple pods to handle rolling upgrades where some pods may not have nodeagent yet.
func IsAWSVPCCNIAvailable(ctx context.Context, logger *zap.Logger, k8sClient kubernetes.Interface) bool {
	pods, err := k8sClient.CoreV1().Pods(AWSNodeNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: AWSNodeLabel,
	})
	if err != nil {
		logger.Debug("Failed to list aws-node pods", zap.Error(err))

		return false
	}

	if len(pods.Items) == 0 {
		logger.Debug("No aws-node pods found")

		return false
	}

	// Check if any pod has the aws-eks-nodeagent container
	for i := range pods.Items {
		if hasNodeagentContainer(pods.Items[i]) {
			logger.Debug("AWS VPC CNI with aws-eks-nodeagent detected",
				zap.String("pod", pods.Items[i].Name))

			return true
		}
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
