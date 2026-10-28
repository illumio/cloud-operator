// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

//nolint:maintidx // table-driven test with comprehensive test cases
func TestParseAWSVPCCNIFlowLog(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantErr       error
		wantSrcIP     string
		wantDstIP     string
		wantProto     string
		wantVerdict   pb.Verdict
		wantDirection pb.TrafficDirection
		wantTier      pb.PolicyTier
	}{
		{
			name:          "valid TCP ACCEPT flow",
			input:         `{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client","caller":"events/events.go:193","msg":"Flow Info: ","Src IP":"10.0.141.167","Src Port":39197,"Dest IP":"172.20.0.10","Dest Port":53,"Proto":"TCP","Verdict":"ACCEPT"}`,
			wantErr:       nil,
			wantSrcIP:     "10.0.141.167",
			wantDstIP:     "172.20.0.10",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_FORWARDED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_TRAFFIC_DIRECTION_UNKNOWN_UNSPECIFIED,
			wantTier:      pb.PolicyTier_POLICY_TIER_UNSPECIFIED,
		},
		{
			name:          "valid TCP DENY flow",
			input:         `{"level":"info","ts":"2024-09-23T12:36:53.604Z","logger":"ebpf-client","caller":"events/events.go:193","msg":"Flow Info: ","Src IP":"10.0.141.167","Src Port":43088,"Dest IP":"172.20.2.72","Dest Port":14220,"Proto":"TCP","Verdict":"DENY"}`,
			wantErr:       nil,
			wantSrcIP:     "10.0.141.167",
			wantDstIP:     "172.20.2.72",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_DROPPED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_TRAFFIC_DIRECTION_UNKNOWN_UNSPECIFIED,
			wantTier:      pb.PolicyTier_POLICY_TIER_UNSPECIFIED,
		},
		{
			name:          "valid UDP flow",
			input:         `{"level":"info","ts":"2024-04-11T02:18:47.938Z","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"192.168.87.155","Src Port":38971,"Dest IP":"64.6.160.1","Dest Port":53,"Proto":"UDP","Verdict":"ACCEPT"}`,
			wantErr:       nil,
			wantSrcIP:     "192.168.87.155",
			wantDstIP:     "64.6.160.1",
			wantProto:     "udp",
			wantVerdict:   pb.Verdict_VERDICT_FORWARDED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_TRAFFIC_DIRECTION_UNKNOWN_UNSPECIFIED,
			wantTier:      pb.PolicyTier_POLICY_TIER_UNSPECIFIED,
		},
		{
			name:          "valid ICMP flow with zero ports",
			input:         `{"level":"info","ts":"2024-02-07T19:07:00.513Z","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"57.20.37.65","Src Port":0,"Dest IP":"100.64.44.16","Dest Port":0,"Proto":"ICMP","Verdict":"DENY"}`,
			wantErr:       nil,
			wantSrcIP:     "57.20.37.65",
			wantDstIP:     "100.64.44.16",
			wantProto:     "icmp",
			wantVerdict:   pb.Verdict_VERDICT_DROPPED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_TRAFFIC_DIRECTION_UNKNOWN_UNSPECIFIED,
			wantTier:      pb.PolicyTier_POLICY_TIER_UNSPECIFIED,
		},
		{
			name:    "not a flow log - different message",
			input:   `{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client","msg":"Starting up","Src IP":"10.0.0.1"}`,
			wantErr: ErrAWSVPCCNINotFlowLog,
		},
		{
			name:    "not a flow log - different logger",
			input:   `{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"other-client","msg":"Flow Info: ","Src IP":"10.0.0.1","Dest IP":"10.0.0.2"}`,
			wantErr: ErrAWSVPCCNINotFlowLog,
		},
		{
			name:    "invalid JSON",
			input:   `not json at all`,
			wantErr: ErrAWSVPCCNINotFlowLog,
		},
		{
			name:    "missing source IP",
			input:   `{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client","msg":"Flow Info: ","Dest IP":"10.0.0.2","Proto":"TCP"}`,
			wantErr: ErrAWSVPCCNIInvalidLog,
		},
		{
			name:    "missing dest IP",
			input:   `{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"10.0.0.1","Proto":"TCP"}`,
			wantErr: ErrAWSVPCCNIInvalidLog,
		},
		{
			name:          "UNKNOWN protocol defaults to TCP",
			input:         `{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"10.0.0.1","Src Port":1234,"Dest IP":"10.0.0.2","Dest Port":80,"Proto":"UNKNOWN","Verdict":"ACCEPT"}`,
			wantErr:       nil,
			wantSrcIP:     "10.0.0.1",
			wantDstIP:     "10.0.0.2",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_FORWARDED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_TRAFFIC_DIRECTION_UNKNOWN_UNSPECIFIED,
			wantTier:      pb.PolicyTier_POLICY_TIER_UNSPECIFIED,
		},
		// New format tests (v1.2.2+) - embedded msg string
		{
			name:          "v1.2.2+ format - TCP ACCEPT egress",
			input:         `{"level":"debug","ts":"2026-04-13T21:18:46.888Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction egress"}`,
			wantErr:       nil,
			wantSrcIP:     "10.0.1.28",
			wantDstIP:     "10.0.1.132",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_FORWARDED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS,
			wantTier:      pb.PolicyTier_POLICY_TIER_UNSPECIFIED,
		},
		{
			name:          "v1.2.2+ format - TCP ACCEPT ingress",
			input:         `{"level":"debug","ts":"2026-04-13T21:18:46.888Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction ingress"}`,
			wantErr:       nil,
			wantSrcIP:     "10.0.1.28",
			wantDstIP:     "10.0.1.132",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_FORWARDED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_INGRESS,
			wantTier:      pb.PolicyTier_POLICY_TIER_UNSPECIFIED,
		},
		{
			name:          "v1.2.2+ format - UDP ACCEPT",
			input:         `{"level":"debug","ts":"2026-04-13T21:20:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 192.168.1.10 Src Port: 53000 Dest IP: 10.0.0.53 Dest Port: 53 Proto UDP Verdict ACCEPT Direction egress"}`,
			wantErr:       nil,
			wantSrcIP:     "192.168.1.10",
			wantDstIP:     "10.0.0.53",
			wantProto:     "udp",
			wantVerdict:   pb.Verdict_VERDICT_FORWARDED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS,
			wantTier:      pb.PolicyTier_POLICY_TIER_UNSPECIFIED,
		},
		{
			name:          "v1.2.2+ format - TCP DENY",
			input:         `{"level":"debug","ts":"2026-04-13T21:25:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.2.50 Src Port: 45000 Dest IP: 10.0.1.100 Dest Port: 443 Proto TCP Verdict DENY Direction egress"}`,
			wantErr:       nil,
			wantSrcIP:     "10.0.2.50",
			wantDstIP:     "10.0.1.100",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_DROPPED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS,
			wantTier:      pb.PolicyTier_POLICY_TIER_UNSPECIFIED,
		},
		{
			name:          "v1.3.0+ format - TCP ACCEPT with Tier",
			input:         `{"level":"debug","ts":"2026-04-13T21:25:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction egress, Tier DEFAULT"}`,
			wantErr:       nil,
			wantSrcIP:     "10.0.1.28",
			wantDstIP:     "10.0.1.132",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_FORWARDED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS,
			wantTier:      pb.PolicyTier_POLICY_TIER_DEFAULT,
		},
		// IPv6 lines put a colon after Proto/Verdict/Direction (v1.2.2+).
		{
			name:          "v1.2.2+ format - IPv6 TCP ACCEPT",
			input:         `{"level":"debug","ts":"2026-04-13T21:25:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 2001:db8::1 Src Port: 55484 Dest IP: 2001:db8::2 Dest Port: 80 Proto: TCP Verdict: ACCEPT Direction: egress"}`,
			wantErr:       nil,
			wantSrcIP:     "2001:db8::1",
			wantDstIP:     "2001:db8::2",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_FORWARDED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS,
			wantTier:      pb.PolicyTier_POLICY_TIER_UNSPECIFIED,
		},
		{
			name:          "v1.3.0+ format - IPv6 UDP DENY with Tier",
			input:         `{"level":"debug","ts":"2026-04-13T21:25:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 2001:db8::1 Src Port: 53000 Dest IP: 2001:db8::53 Dest Port: 53 Proto: UDP Verdict: DENY Direction: ingress Tier: DEFAULT"}`,
			wantErr:       nil,
			wantSrcIP:     "2001:db8::1",
			wantDstIP:     "2001:db8::53",
			wantProto:     "udp",
			wantVerdict:   pb.Verdict_VERDICT_DROPPED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_INGRESS,
			wantTier:      pb.PolicyTier_POLICY_TIER_DEFAULT,
		},
		// v1.3.0+ logs the policy tier that decided the verdict.
		{
			name:          "v1.3.0+ format - ADMIN tier",
			input:         `{"level":"debug","ts":"2026-04-13T21:25:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.2.50 Src Port: 45000 Dest IP: 10.0.1.100 Dest Port: 443 Proto TCP Verdict DENY Direction ingress, Tier ADMIN"}`,
			wantErr:       nil,
			wantSrcIP:     "10.0.2.50",
			wantDstIP:     "10.0.1.100",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_DROPPED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_INGRESS,
			wantTier:      pb.PolicyTier_POLICY_TIER_ADMIN,
		},
		{
			name:          "v1.3.0+ format - NETWORK_POLICY tier",
			input:         `{"level":"debug","ts":"2026-04-13T21:25:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction ingress, Tier NETWORK_POLICY"}`,
			wantErr:       nil,
			wantSrcIP:     "10.0.1.28",
			wantDstIP:     "10.0.1.132",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_FORWARDED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_INGRESS,
			wantTier:      pb.PolicyTier_POLICY_TIER_NETWORK_POLICY,
		},
		{
			name:          "v1.3.0+ format - BASELINE tier",
			input:         `{"level":"debug","ts":"2026-04-13T21:25:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 192.168.1.10 Src Port: 53000 Dest IP: 10.0.0.53 Dest Port: 53 Proto UDP Verdict ACCEPT Direction egress, Tier BASELINE"}`,
			wantErr:       nil,
			wantSrcIP:     "192.168.1.10",
			wantDstIP:     "10.0.0.53",
			wantProto:     "udp",
			wantVerdict:   pb.Verdict_VERDICT_FORWARDED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS,
			wantTier:      pb.PolicyTier_POLICY_TIER_BASELINE,
		},
		{
			name:          "v1.3.0+ format - ERROR tier",
			input:         `{"level":"debug","ts":"2026-04-13T21:25:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.2.50 Src Port: 45000 Dest IP: 10.0.1.100 Dest Port: 443 Proto TCP Verdict DENY Direction egress, Tier ERROR"}`,
			wantErr:       nil,
			wantSrcIP:     "10.0.2.50",
			wantDstIP:     "10.0.1.100",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_DROPPED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS,
			wantTier:      pb.PolicyTier_POLICY_TIER_ERROR,
		},
		{
			name:          "v1.3.0+ format - IPv6 NETWORK_POLICY tier",
			input:         `{"level":"debug","ts":"2026-04-13T21:25:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 2001:db8::1 Src Port: 55484 Dest IP: 2001:db8::2 Dest Port: 80 Proto: TCP Verdict: DENY Direction: egress Tier: NETWORK_POLICY"}`,
			wantErr:       nil,
			wantSrcIP:     "2001:db8::1",
			wantDstIP:     "2001:db8::2",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_DROPPED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS,
			wantTier:      pb.PolicyTier_POLICY_TIER_NETWORK_POLICY,
		},
		{
			name:          "v1.3.0+ format - unrecognized tier is sent as unknown",
			input:         `{"level":"debug","ts":"2026-04-13T21:25:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction egress, Tier FUTURE"}`,
			wantErr:       nil,
			wantSrcIP:     "10.0.1.28",
			wantDstIP:     "10.0.1.132",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_FORWARDED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS,
			wantTier:      pb.PolicyTier_POLICY_TIER_UNSPECIFIED,
		},
		// EXPIRED/DELETED is not an allow/deny decision, so it is not sent as a flow.
		{
			name:    "EXPIRED/DELETED verdict is not a flow",
			input:   `{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"10.0.0.1","Src Port":1234,"Dest IP":"10.0.0.2","Dest Port":80,"Proto":"TCP","Verdict":"EXPIRED/DELETED"}`,
			wantErr: ErrAWSVPCCNINotFlowLog,
		},
		{
			name:    "v1.2.2+ format - EXPIRED/DELETED verdict is not a flow",
			input:   `{"level":"debug","ts":"2026-04-13T21:25:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict EXPIRED/DELETED Direction ingress"}`,
			wantErr: ErrAWSVPCCNINotFlowLog,
		},
		{
			name:          "missing verdict is sent as unknown",
			input:         `{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"10.0.0.1","Src Port":1234,"Dest IP":"10.0.0.2","Dest Port":80,"Proto":"TCP"}`,
			wantErr:       nil,
			wantSrcIP:     "10.0.0.1",
			wantDstIP:     "10.0.0.2",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_UNKNOWN_UNSPECIFIED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_TRAFFIC_DIRECTION_UNKNOWN_UNSPECIFIED,
			wantTier:      pb.PolicyTier_POLICY_TIER_UNSPECIFIED,
		},
		{
			name:          "v1.2.2+ format - missing direction is sent as unknown",
			input:         `{"level":"debug","ts":"2026-04-13T21:25:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT"}`,
			wantErr:       nil,
			wantSrcIP:     "10.0.1.28",
			wantDstIP:     "10.0.1.132",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_FORWARDED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_TRAFFIC_DIRECTION_UNKNOWN_UNSPECIFIED,
			wantTier:      pb.PolicyTier_POLICY_TIER_UNSPECIFIED,
		},
		{
			name:          "v1.2.2+ format - missing verdict is sent as unknown",
			input:         `{"level":"debug","ts":"2026-04-13T21:25:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Direction egress"}`,
			wantErr:       nil,
			wantSrcIP:     "10.0.1.28",
			wantDstIP:     "10.0.1.132",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_UNKNOWN_UNSPECIFIED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS,
			wantTier:      pb.PolicyTier_POLICY_TIER_UNSPECIFIED,
		},
		{
			name:          "v1.3.0+ format - IPv6 missing verdict with Tier is sent as unknown",
			input:         `{"level":"debug","ts":"2026-04-13T21:25:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 2001:db8::1 Src Port: 53000 Dest IP: 2001:db8::53 Dest Port: 53 Proto: UDP Direction: ingress Tier: DEFAULT"}`,
			wantErr:       nil,
			wantSrcIP:     "2001:db8::1",
			wantDstIP:     "2001:db8::53",
			wantProto:     "udp",
			wantVerdict:   pb.Verdict_VERDICT_UNKNOWN_UNSPECIFIED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_INGRESS,
			wantTier:      pb.PolicyTier_POLICY_TIER_DEFAULT,
		},
		{
			name:          "v1.2.2+ format - missing verdict and direction are sent as unknown",
			input:         `{"level":"debug","ts":"2026-04-13T21:25:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP"}`,
			wantErr:       nil,
			wantSrcIP:     "10.0.1.28",
			wantDstIP:     "10.0.1.132",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_UNKNOWN_UNSPECIFIED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_TRAFFIC_DIRECTION_UNKNOWN_UNSPECIFIED,
			wantTier:      pb.PolicyTier_POLICY_TIER_UNSPECIFIED,
		},
		// The IPv4 v1.3.0+ ", Tier DEFAULT" follows whichever field is logged last.
		{
			name:          "v1.3.0+ format - missing direction with Tier keeps the verdict",
			input:         `{"level":"debug","ts":"2026-04-13T21:25:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict DENY, Tier DEFAULT"}`,
			wantErr:       nil,
			wantSrcIP:     "10.0.1.28",
			wantDstIP:     "10.0.1.132",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_DROPPED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_TRAFFIC_DIRECTION_UNKNOWN_UNSPECIFIED,
			wantTier:      pb.PolicyTier_POLICY_TIER_DEFAULT,
		},
		{
			name:          "v1.3.0+ format - missing verdict and direction with Tier are sent as unknown",
			input:         `{"level":"debug","ts":"2026-04-13T21:25:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP, Tier DEFAULT"}`,
			wantErr:       nil,
			wantSrcIP:     "10.0.1.28",
			wantDstIP:     "10.0.1.132",
			wantProto:     "tcp",
			wantVerdict:   pb.Verdict_VERDICT_UNKNOWN_UNSPECIFIED,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_TRAFFIC_DIRECTION_UNKNOWN_UNSPECIFIED,
			wantTier:      pb.PolicyTier_POLICY_TIER_DEFAULT,
		},
		{
			name:    "v1.2.2+ format - invalid msg (missing fields)",
			input:   `{"level":"debug","ts":"2026-04-13T21:18:46.888Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.1.28"}`,
			wantErr: ErrAWSVPCCNIInvalidLog,
		},
		{
			name:    "missing timestamp",
			input:   `{"level":"info","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"10.0.0.1","Src Port":1234,"Dest IP":"10.0.0.2","Dest Port":80,"Proto":"TCP","Verdict":"ACCEPT"}`,
			wantErr: ErrAWSVPCCNIInvalidTimestamp,
		},
		{
			name:    "invalid timestamp format",
			input:   `{"level":"info","ts":"not-a-timestamp","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"10.0.0.1","Src Port":1234,"Dest IP":"10.0.0.2","Dest Port":80,"Proto":"TCP","Verdict":"ACCEPT"}`,
			wantErr: ErrAWSVPCCNIInvalidTimestamp,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flow, err := ParseAWSVPCCNIFlowLog(tt.input)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("expected error %v, got %v", tt.wantErr, err)
				}

				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)

				return
			}

			if flow == nil {
				t.Error("expected flow, got nil")

				return
			}

			// Check IPs using the IP struct fields (a nil layer3 fails these too)
			checkEqual(t, "src IP", flow.GetLayer3().GetSource(), tt.wantSrcIP)
			checkEqual(t, "dst IP", flow.GetLayer3().GetDestination(), tt.wantDstIP)

			// Check protocol in layer4
			if flow.GetLayer4() == nil {
				t.Error("expected layer4, got nil")

				return
			}

			checkEqual(t, "verdict", flow.GetVerdict(), tt.wantVerdict)
			checkEqual(t, "direction", flow.GetTrafficDirection(), tt.wantDirection)
			checkEqual(t, "tier", flow.GetPolicyTier(), tt.wantTier)
		})
	}
}

// checkEqual reports a mismatch of one parsed field; a helper keeps the
// table-driven parser tests under the gocognit limit.
func checkEqual[T comparable](t *testing.T, field string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}

func TestParseOldFormat(t *testing.T) {
	tests := []struct {
		name         string
		log          AWSVPCCNIFlowLog
		wantOk       bool
		wantSrcIP    string
		wantSrcPort  uint32
		wantDestIP   string
		wantDestPort uint32
		wantProto    string
	}{
		{
			name: "valid TCP flow",
			log: AWSVPCCNIFlowLog{
				SrcIP:    "10.0.141.167",
				SrcPort:  39197,
				DestIP:   "172.20.0.10",
				DestPort: 53,
				Proto:    "TCP",
			},
			wantOk:       true,
			wantSrcIP:    "10.0.141.167",
			wantSrcPort:  39197,
			wantDestIP:   "172.20.0.10",
			wantDestPort: 53,
			wantProto:    "TCP",
		},
		{
			name: "missing source IP",
			log: AWSVPCCNIFlowLog{
				DestIP:   "172.20.0.10",
				DestPort: 53,
				Proto:    "TCP",
			},
			wantOk: false,
		},
		{
			name: "missing dest IP",
			log: AWSVPCCNIFlowLog{
				SrcIP:   "10.0.141.167",
				SrcPort: 39197,
				Proto:   "TCP",
			},
			wantOk: false,
		},
		{
			name:   "empty log",
			log:    AWSVPCCNIFlowLog{},
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srcIP, srcPort, destIP, destPort, proto, ok := parseOldFormat(&tt.log)

			if ok != tt.wantOk {
				t.Errorf("parseOldFormat() ok = %v, want %v", ok, tt.wantOk)

				return
			}

			if !tt.wantOk {
				return
			}

			if srcIP != tt.wantSrcIP {
				t.Errorf("srcIP = %v, want %v", srcIP, tt.wantSrcIP)
			}

			if srcPort != tt.wantSrcPort {
				t.Errorf("srcPort = %v, want %v", srcPort, tt.wantSrcPort)
			}

			if destIP != tt.wantDestIP {
				t.Errorf("destIP = %v, want %v", destIP, tt.wantDestIP)
			}

			if destPort != tt.wantDestPort {
				t.Errorf("destPort = %v, want %v", destPort, tt.wantDestPort)
			}

			if proto != tt.wantProto {
				t.Errorf("proto = %v, want %v", proto, tt.wantProto)
			}
		})
	}
}

func TestParseFlowFromMsg(t *testing.T) {
	tests := []struct {
		name          string
		msg           string
		wantOk        bool
		wantSrcIP     string
		wantSrcPort   uint32
		wantDestIP    string
		wantDestPort  uint32
		wantProto     string
		wantVerdict   string
		wantDirection string
		wantTier      string
	}{
		{
			name:          "valid TCP ACCEPT egress",
			msg:           "Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction egress",
			wantOk:        true,
			wantSrcIP:     "10.0.1.28",
			wantSrcPort:   55484,
			wantDestIP:    "10.0.1.132",
			wantDestPort:  80,
			wantProto:     "TCP",
			wantVerdict:   "ACCEPT",
			wantDirection: "egress",
			wantTier:      "",
		},
		{
			name:          "valid UDP DENY",
			msg:           "Flow Info: Src IP: 192.168.1.10 Src Port: 53000 Dest IP: 10.0.0.53 Dest Port: 53 Proto UDP Verdict DENY Direction ingress",
			wantOk:        true,
			wantSrcIP:     "192.168.1.10",
			wantSrcPort:   53000,
			wantDestIP:    "10.0.0.53",
			wantDestPort:  53,
			wantProto:     "UDP",
			wantVerdict:   "DENY",
			wantDirection: "ingress",
			wantTier:      "",
		},
		{
			name:          "valid TCP ACCEPT with Tier (v1.3.0+)",
			msg:           "Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction egress, Tier DEFAULT",
			wantOk:        true,
			wantSrcIP:     "10.0.1.28",
			wantSrcPort:   55484,
			wantDestIP:    "10.0.1.132",
			wantDestPort:  80,
			wantProto:     "TCP",
			wantVerdict:   "ACCEPT",
			wantDirection: "egress",
			wantTier:      "DEFAULT",
		},
		{
			name:          "valid IPv6 TCP ACCEPT (colon after Proto and Verdict)",
			msg:           "Flow Info: Src IP: 2001:db8::1 Src Port: 55484 Dest IP: 2001:db8::2 Dest Port: 80 Proto: TCP Verdict: ACCEPT Direction: egress",
			wantOk:        true,
			wantSrcIP:     "2001:db8::1",
			wantSrcPort:   55484,
			wantDestIP:    "2001:db8::2",
			wantDestPort:  80,
			wantProto:     "TCP",
			wantVerdict:   "ACCEPT",
			wantDirection: "egress",
			wantTier:      "",
		},
		{
			name:          "valid IPv6 UDP DENY with Tier (v1.3.0+)",
			msg:           "Flow Info: Src IP: 2001:db8::1 Src Port: 53000 Dest IP: 2001:db8::53 Dest Port: 53 Proto: UDP Verdict: DENY Direction: ingress Tier: DEFAULT",
			wantOk:        true,
			wantSrcIP:     "2001:db8::1",
			wantSrcPort:   53000,
			wantDestIP:    "2001:db8::53",
			wantDestPort:  53,
			wantProto:     "UDP",
			wantVerdict:   "DENY",
			wantDirection: "ingress",
			wantTier:      "DEFAULT",
		},
		{
			name:          "valid without Direction",
			msg:           "Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT",
			wantOk:        true,
			wantSrcIP:     "10.0.1.28",
			wantSrcPort:   55484,
			wantDestIP:    "10.0.1.132",
			wantDestPort:  80,
			wantProto:     "TCP",
			wantVerdict:   "ACCEPT",
			wantDirection: "",
			wantTier:      "",
		},
		{
			name:          "valid without Verdict",
			msg:           "Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Direction egress",
			wantOk:        true,
			wantSrcIP:     "10.0.1.28",
			wantSrcPort:   55484,
			wantDestIP:    "10.0.1.132",
			wantDestPort:  80,
			wantProto:     "TCP",
			wantVerdict:   "",
			wantDirection: "egress",
			wantTier:      "",
		},
		{
			name:          "valid IPv6 without Verdict with Tier (v1.3.0+)",
			msg:           "Flow Info: Src IP: 2001:db8::1 Src Port: 53000 Dest IP: 2001:db8::53 Dest Port: 53 Proto: UDP Direction: ingress Tier: DEFAULT",
			wantOk:        true,
			wantSrcIP:     "2001:db8::1",
			wantSrcPort:   53000,
			wantDestIP:    "2001:db8::53",
			wantDestPort:  53,
			wantProto:     "UDP",
			wantVerdict:   "",
			wantDirection: "ingress",
			wantTier:      "DEFAULT",
		},
		{
			name:          "valid without Verdict or Direction",
			msg:           "Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP",
			wantOk:        true,
			wantSrcIP:     "10.0.1.28",
			wantSrcPort:   55484,
			wantDestIP:    "10.0.1.132",
			wantDestPort:  80,
			wantProto:     "TCP",
			wantVerdict:   "",
			wantDirection: "",
			wantTier:      "",
		},
		{
			name:          "valid without Direction with Tier (v1.3.0+)",
			msg:           "Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict DENY, Tier DEFAULT",
			wantOk:        true,
			wantSrcIP:     "10.0.1.28",
			wantSrcPort:   55484,
			wantDestIP:    "10.0.1.132",
			wantDestPort:  80,
			wantProto:     "TCP",
			wantVerdict:   "DENY",
			wantDirection: "",
			wantTier:      "DEFAULT",
		},
		{
			name:          "valid without Verdict or Direction with Tier (v1.3.0+)",
			msg:           "Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP, Tier DEFAULT",
			wantOk:        true,
			wantSrcIP:     "10.0.1.28",
			wantSrcPort:   55484,
			wantDestIP:    "10.0.1.132",
			wantDestPort:  80,
			wantProto:     "TCP",
			wantVerdict:   "",
			wantDirection: "",
			wantTier:      "DEFAULT",
		},
		{
			name:          "valid NETWORK_POLICY Tier (v1.3.0+)",
			msg:           "Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict DENY Direction ingress, Tier NETWORK_POLICY",
			wantOk:        true,
			wantSrcIP:     "10.0.1.28",
			wantSrcPort:   55484,
			wantDestIP:    "10.0.1.132",
			wantDestPort:  80,
			wantProto:     "TCP",
			wantVerdict:   "DENY",
			wantDirection: "ingress",
			wantTier:      "NETWORK_POLICY",
		},
		{
			name:          "valid IPv6 ADMIN Tier (v1.3.0+)",
			msg:           "Flow Info: Src IP: 2001:db8::1 Src Port: 55484 Dest IP: 2001:db8::2 Dest Port: 80 Proto: TCP Verdict: ACCEPT Direction: egress Tier: ADMIN",
			wantOk:        true,
			wantSrcIP:     "2001:db8::1",
			wantSrcPort:   55484,
			wantDestIP:    "2001:db8::2",
			wantDestPort:  80,
			wantProto:     "TCP",
			wantVerdict:   "ACCEPT",
			wantDirection: "egress",
			wantTier:      "ADMIN",
		},
		{
			name:          "valid without Verdict with Tier (v1.3.0+)",
			msg:           "Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Direction egress, Tier BASELINE",
			wantOk:        true,
			wantSrcIP:     "10.0.1.28",
			wantSrcPort:   55484,
			wantDestIP:    "10.0.1.132",
			wantDestPort:  80,
			wantProto:     "TCP",
			wantVerdict:   "",
			wantDirection: "egress",
			wantTier:      "BASELINE",
		},
		{
			name:          "valid with an empty Tier (v1.3.0+)",
			msg:           "Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction egress, Tier ",
			wantOk:        true,
			wantSrcIP:     "10.0.1.28",
			wantSrcPort:   55484,
			wantDestIP:    "10.0.1.132",
			wantDestPort:  80,
			wantProto:     "TCP",
			wantVerdict:   "ACCEPT",
			wantDirection: "egress",
			wantTier:      "",
		},
		{
			name:   "missing required fields",
			msg:    "Flow Info: Src IP: 10.0.1.28",
			wantOk: false,
		},
		{
			name:   "empty message",
			msg:    "",
			wantOk: false,
		},
		{
			name:   "not a flow message - missing Flow Info prefix",
			msg:    "Starting up ebpf client",
			wantOk: false,
		},
		{
			name:   "missing Flow Info prefix",
			msg:    "Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT",
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srcIP, srcPort, destIP, destPort, proto, verdict, direction, tier, ok := parseFlowFromMsg(tt.msg)

			if ok != tt.wantOk {
				t.Errorf("parseFlowFromMsg() ok = %v, want %v", ok, tt.wantOk)

				return
			}

			if !tt.wantOk {
				return
			}

			checkEqual(t, "srcIP", srcIP, tt.wantSrcIP)
			checkEqual(t, "srcPort", srcPort, tt.wantSrcPort)
			checkEqual(t, "destIP", destIP, tt.wantDestIP)
			checkEqual(t, "destPort", destPort, tt.wantDestPort)
			checkEqual(t, "proto", proto, tt.wantProto)
			checkEqual(t, "verdict", verdict, tt.wantVerdict)
			checkEqual(t, "direction", direction, tt.wantDirection)
			checkEqual(t, "tier", tier, tt.wantTier)
		})
	}
}

func TestIsIPv6(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"10.0.0.1", false},
		{"192.168.1.1", false},
		{"::1", true},
		{"2001:db8::1", true},
		{"fe80::1", true},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			got := isIPv6(tt.addr)
			if got != tt.want {
				t.Errorf("isIPv6(%s) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestIsAWSVPCCNIAvailable(t *testing.T) {
	logger := zap.NewNop()
	ctx := context.Background()

	tests := []struct {
		name     string
		pods     []corev1.Pod
		expected bool
	}{
		{
			name:     "no aws-node pods",
			pods:     []corev1.Pod{},
			expected: false,
		},
		{
			name: "aws-node pod without nodeagent container",
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "aws-node-abc",
						Namespace: "kube-system",
						Labels: map[string]string{
							"k8s-app": "aws-node",
						},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "aws-node"},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "aws-node pod with nodeagent container",
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "aws-node-xyz",
						Namespace: "kube-system",
						Labels: map[string]string{
							"k8s-app": "aws-node",
						},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "aws-node"},
							{Name: "aws-eks-nodeagent"},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "multiple aws-node pods with nodeagent",
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "aws-node-aaa",
						Namespace: "kube-system",
						Labels: map[string]string{
							"k8s-app": "aws-node",
						},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "aws-node"},
							{Name: "aws-eks-nodeagent"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "aws-node-bbb",
						Namespace: "kube-system",
						Labels: map[string]string{
							"k8s-app": "aws-node",
						},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "aws-node"},
							{Name: "aws-eks-nodeagent"},
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			for _, pod := range tt.pods {
				_, err := client.CoreV1().Pods(pod.Namespace).Create(ctx, &pod, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("failed to create pod: %v", err)
				}
			}

			result := IsAWSVPCCNIAvailable(ctx, logger, client)
			if result != tt.expected {
				t.Errorf("IsAWSVPCCNIAvailable() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// countingSink records how many flows were cached, to assert CacheFlowLine's
// parse -> stale-filter -> cache behavior.
type countingSink struct {
	cached   int
	received int
	err      error
}

func (s *countingSink) CacheFlow(_ context.Context, _ pb.Flow) error {
	if s.err != nil {
		return s.err
	}

	s.cached++

	return nil
}

func (s *countingSink) IncrementFlowsReceived() { s.received++ }

func flowLineAt(ts string) string {
	return `{"level":"info","ts":"` + ts + `","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"10.0.1.1","Src Port":80,"Dest IP":"10.0.1.2","Dest Port":443,"Proto":"TCP","Verdict":"ACCEPT"}`
}

func TestCacheFlowLine(t *testing.T) {
	logger := zap.NewNop()
	recent := time.Now().Add(-time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
	stale := "2024-09-23T12:36:53.562Z"
	notBefore := time.Now().Add(-MaxFlowAge)

	tests := []struct {
		name         string
		line         string
		notBefore    time.Time
		sinkErr      error
		wantCached   bool
		wantReceived int
	}{
		{
			name:         "recent flow is cached",
			line:         flowLineAt(recent),
			notBefore:    notBefore,
			wantCached:   true,
			wantReceived: 1,
		},
		{
			name:      "stale flow is dropped",
			line:      flowLineAt(stale),
			notBefore: notBefore,
		},
		{
			name:      "non-flow line is skipped",
			line:      `{"level":"info","ts":"` + recent + `","logger":"other","msg":"housekeeping"}`,
			notBefore: notBefore,
		},
		{
			name:       "zero notBefore disables the filter",
			line:       flowLineAt(stale),
			notBefore:  time.Time{},
			wantCached: true, wantReceived: 1,
		},
		{
			name:      "cache error is swallowed (best-effort)",
			line:      flowLineAt(recent),
			notBefore: notBefore,
			sinkErr:   errors.New("cache full"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := &countingSink{err: tt.sinkErr}

			cached, err := CacheFlowLine(context.Background(), sink, tt.line, tt.notBefore, logger)
			if err != nil {
				t.Fatalf("CacheFlowLine() unexpected error: %v", err)
			}

			if cached != tt.wantCached {
				t.Errorf("cached = %v, want %v", cached, tt.wantCached)
			}

			if sink.received != tt.wantReceived {
				t.Errorf("IncrementFlowsReceived calls = %d, want %d", sink.received, tt.wantReceived)
			}
		})
	}
}

func TestCacheFlowLine_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sink := &countingSink{}

	_, err := CacheFlowLine(ctx, sink, flowLineAt("2024-09-23T12:36:53.562Z"), time.Time{}, zap.NewNop())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CacheFlowLine() error = %v, want context.Canceled", err)
	}
}
