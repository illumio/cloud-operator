// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"testing"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestParseVPCCNIFlowLog(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantErr   error
		wantSrcIP string
		wantDstIP string
		wantProto string
	}{
		{
			name:      "valid TCP ACCEPT flow",
			input:     `{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client","caller":"events/events.go:193","msg":"Flow Info: ","Src IP":"10.0.141.167","Src Port":39197,"Dest IP":"172.20.0.10","Dest Port":53,"Proto":"TCP","Verdict":"ACCEPT"}`,
			wantErr:   nil,
			wantSrcIP: "10.0.141.167",
			wantDstIP: "172.20.0.10",
			wantProto: "tcp",
		},
		{
			name:      "valid TCP DENY flow",
			input:     `{"level":"info","ts":"2024-09-23T12:36:53.604Z","logger":"ebpf-client","caller":"events/events.go:193","msg":"Flow Info: ","Src IP":"10.0.141.167","Src Port":43088,"Dest IP":"172.20.2.72","Dest Port":14220,"Proto":"TCP","Verdict":"DENY"}`,
			wantErr:   nil,
			wantSrcIP: "10.0.141.167",
			wantDstIP: "172.20.2.72",
			wantProto: "tcp",
		},
		{
			name:      "valid UDP flow",
			input:     `{"level":"info","ts":"2024-04-11T02:18:47.938Z","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"192.168.87.155","Src Port":38971,"Dest IP":"64.6.160.1","Dest Port":53,"Proto":"UDP","Verdict":"ACCEPT"}`,
			wantErr:   nil,
			wantSrcIP: "192.168.87.155",
			wantDstIP: "64.6.160.1",
			wantProto: "udp",
		},
		{
			name:      "valid ICMP flow with zero ports",
			input:     `{"level":"info","ts":"2024-02-07T19:07:00.513Z","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"57.20.37.65","Src Port":0,"Dest IP":"100.64.44.16","Dest Port":0,"Proto":"ICMP","Verdict":"DENY"}`,
			wantErr:   nil,
			wantSrcIP: "57.20.37.65",
			wantDstIP: "100.64.44.16",
			wantProto: "icmp",
		},
		{
			name:    "not a flow log - different message",
			input:   `{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client","msg":"Starting up","Src IP":"10.0.0.1"}`,
			wantErr: ErrVPCCNINotFlowLog,
		},
		{
			name:    "not a flow log - different logger",
			input:   `{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"other-client","msg":"Flow Info: ","Src IP":"10.0.0.1","Dest IP":"10.0.0.2"}`,
			wantErr: ErrVPCCNINotFlowLog,
		},
		{
			name:    "invalid JSON",
			input:   `not json at all`,
			wantErr: ErrVPCCNINotFlowLog,
		},
		{
			name:    "missing source IP",
			input:   `{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client","msg":"Flow Info: ","Dest IP":"10.0.0.2","Proto":"TCP"}`,
			wantErr: ErrVPCCNIInvalidLog,
		},
		{
			name:    "missing dest IP",
			input:   `{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"10.0.0.1","Proto":"TCP"}`,
			wantErr: ErrVPCCNIInvalidLog,
		},
		{
			name:      "UNKNOWN protocol defaults to TCP",
			input:     `{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"10.0.0.1","Src Port":1234,"Dest IP":"10.0.0.2","Dest Port":80,"Proto":"UNKNOWN","Verdict":"ACCEPT"}`,
			wantErr:   nil,
			wantSrcIP: "10.0.0.1",
			wantDstIP: "10.0.0.2",
			wantProto: "tcp",
		},
		// New format tests (v1.2.2+) - embedded msg string
		{
			name:      "v1.2.2+ format - TCP ACCEPT egress",
			input:     `{"level":"debug","ts":"2026-04-13T21:18:46.888Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction egress"}`,
			wantErr:   nil,
			wantSrcIP: "10.0.1.28",
			wantDstIP: "10.0.1.132",
			wantProto: "tcp",
		},
		{
			name:      "v1.2.2+ format - TCP ACCEPT ingress",
			input:     `{"level":"debug","ts":"2026-04-13T21:18:46.888Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction ingress"}`,
			wantErr:   nil,
			wantSrcIP: "10.0.1.28",
			wantDstIP: "10.0.1.132",
			wantProto: "tcp",
		},
		{
			name:      "v1.2.2+ format - UDP ACCEPT",
			input:     `{"level":"debug","ts":"2026-04-13T21:20:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 192.168.1.10 Src Port: 53000 Dest IP: 10.0.0.53 Dest Port: 53 Proto UDP Verdict ACCEPT Direction egress"}`,
			wantErr:   nil,
			wantSrcIP: "192.168.1.10",
			wantDstIP: "10.0.0.53",
			wantProto: "udp",
		},
		{
			name:      "v1.2.2+ format - TCP DENY",
			input:     `{"level":"debug","ts":"2026-04-13T21:25:00.000Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.2.50 Src Port: 45000 Dest IP: 10.0.1.100 Dest Port: 443 Proto TCP Verdict DENY Direction egress"}`,
			wantErr:   nil,
			wantSrcIP: "10.0.2.50",
			wantDstIP: "10.0.1.100",
			wantProto: "tcp",
		},
		{
			name:    "v1.2.2+ format - invalid msg (missing fields)",
			input:   `{"level":"debug","ts":"2026-04-13T21:18:46.888Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.1.28"}`,
			wantErr: ErrVPCCNIInvalidLog,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flow, err := ParseVPCCNIFlowLog(tt.input)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("expected error %v, got nil", tt.wantErr)
					return
				}
				if err != tt.wantErr {
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

			// Verify layer3 IPs
			if flow.Layer3 == nil {
				t.Error("expected layer3, got nil")
				return
			}

			// Check IPs using the IP struct fields
			if flow.Layer3.Source != tt.wantSrcIP {
				t.Errorf("expected src IP %s, got %s", tt.wantSrcIP, flow.Layer3.Source)
			}
			if flow.Layer3.Destination != tt.wantDstIP {
				t.Errorf("expected dst IP %s, got %s", tt.wantDstIP, flow.Layer3.Destination)
			}

			// Check protocol in layer4
			if flow.Layer4 == nil {
				t.Error("expected layer4, got nil")
				return
			}
		})
	}
}

func TestParseFlowFromMsg(t *testing.T) {
	tests := []struct {
		name         string
		msg          string
		wantOk       bool
		wantSrcIP    string
		wantSrcPort  uint32
		wantDestIP   string
		wantDestPort uint32
		wantProto    string
		wantVerdict  string
	}{
		{
			name:         "valid TCP ACCEPT egress",
			msg:          "Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction egress",
			wantOk:       true,
			wantSrcIP:    "10.0.1.28",
			wantSrcPort:  55484,
			wantDestIP:   "10.0.1.132",
			wantDestPort: 80,
			wantProto:    "TCP",
			wantVerdict:  "ACCEPT",
		},
		{
			name:         "valid UDP DENY",
			msg:          "Flow Info: Src IP: 192.168.1.10 Src Port: 53000 Dest IP: 10.0.0.53 Dest Port: 53 Proto UDP Verdict DENY Direction ingress",
			wantOk:       true,
			wantSrcIP:    "192.168.1.10",
			wantSrcPort:  53000,
			wantDestIP:   "10.0.0.53",
			wantDestPort: 53,
			wantProto:    "UDP",
			wantVerdict:  "DENY",
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
			name:   "not a flow message",
			msg:    "Starting up ebpf client",
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srcIP, srcPort, destIP, destPort, proto, verdict, ok := parseFlowFromMsg(tt.msg)

			if ok != tt.wantOk {
				t.Errorf("parseFlowFromMsg() ok = %v, want %v", ok, tt.wantOk)
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
			if verdict != tt.wantVerdict {
				t.Errorf("verdict = %v, want %v", verdict, tt.wantVerdict)
			}
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

func TestIsVPCCNIAvailable(t *testing.T) {
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

			result := IsVPCCNIAvailable(ctx, logger, client)
			if result != tt.expected {
				t.Errorf("IsVPCCNIAvailable() = %v, want %v", result, tt.expected)
			}
		})
	}
}
