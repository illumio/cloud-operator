// Copyright 2026 Illumio, Inc. All Rights Reserved.

package calico

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	goldmanepb "github.com/illumio/cloud-operator/api/illumio/cloud/goldmane/v1"
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

const testStartTime = 1760000000

func goldmaneFlow(reporter goldmanepb.Reporter, action goldmanepb.Action, hits ...*goldmanepb.PolicyHit) *goldmanepb.Flow {
	return &goldmanepb.Flow{
		Key: &goldmanepb.FlowKey{
			SourceName:           "frontend-7d9f8c-*",
			SourceNamespace:      "shop",
			SourceType:           goldmanepb.EndpointType_WorkloadEndpoint,
			DestName:             "cart-5b6c4d-*",
			DestNamespace:        "shop",
			DestType:             goldmanepb.EndpointType_WorkloadEndpoint,
			DestPort:             8080,
			DestServiceName:      "cart",
			DestServiceNamespace: "shop",
			DestServicePortName:  "http",
			DestServicePort:      80,
			Proto:                "tcp",
			Reporter:             reporter,
			Action:               action,
			Policies:             &goldmanepb.PolicyTrace{EnforcedPolicies: hits},
		},
		StartTime:    testStartTime,
		EndTime:      testStartTime + 15,
		SourceLabels: []string{"app=frontend"},
		DestLabels:   []string{"app=cart"},
		SourceIps:    []string{"10.0.1.12", "10.0.1.13"},
		DestIps:      []string{"10.0.2.40"},
	}
}

func policyHit(kind goldmanepb.PolicyKind, name string, action goldmanepb.Action) *goldmanepb.PolicyHit {
	return &goldmanepb.PolicyHit{Kind: kind, Namespace: "shop", Name: name, Tier: "default", Action: action}
}

func TestConvertGoldmaneFlow_Full(t *testing.T) {
	flow := convertGoldmaneFlow(goldmaneFlow(goldmanepb.Reporter_Src, goldmanepb.Action_Allow,
		policyHit(goldmanepb.PolicyKind_NetworkPolicy, "allow-frontend-to-cart", goldmanepb.Action_Allow)))

	want := &pb.CalicoFlow{
		Time:             timestamppb.New(time.Unix(testStartTime, 0)),
		TrafficDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS,
		Verdict:          pb.Verdict_VERDICT_FORWARDED,
		SourceIps:        []string{"10.0.1.12", "10.0.1.13"},
		DestinationIps:   []string{"10.0.2.40"},
		Layer4:           &pb.Layer4{Protocol: &pb.Layer4_Tcp{Tcp: &pb.TCP{DestinationPort: 8080, Flags: &pb.TCPFlags{}}}},
		SourceEndpoint: &pb.CalicoEndpoint{
			Namespace: "shop", Labels: []string{"app=frontend"}, AggregatedName: "frontend-7d9f8c-*",
		},
		DestinationEndpoint: &pb.CalicoEndpoint{
			Namespace: "shop", Labels: []string{"app=cart"}, AggregatedName: "cart-5b6c4d-*",
		},
		DestinationService: &pb.Service{Name: "cart", Namespace: "shop"},
		EgressAllowedBy:    []*pb.Policy{{Name: "allow-frontend-to-cart", Namespace: "shop", Kind: "NetworkPolicy"}},
	}

	assert.True(t, proto.Equal(want, flow), "got %v", flow)
}

func TestConvertGoldmaneFlow_DirectionAndVerdictSelectPolicyField(t *testing.T) {
	tests := []struct {
		name          string
		reporter      goldmanepb.Reporter
		action        goldmanepb.Action
		wantDirection pb.TrafficDirection
		wantVerdict   pb.Verdict
		policies      func(*pb.CalicoFlow) []*pb.Policy
	}{
		{
			name: "source reporter allow", reporter: goldmanepb.Reporter_Src, action: goldmanepb.Action_Allow,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS, wantVerdict: pb.Verdict_VERDICT_FORWARDED,
			policies: (*pb.CalicoFlow).GetEgressAllowedBy,
		},
		{
			name: "destination reporter allow", reporter: goldmanepb.Reporter_Dst, action: goldmanepb.Action_Allow,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_INGRESS, wantVerdict: pb.Verdict_VERDICT_FORWARDED,
			policies: (*pb.CalicoFlow).GetIngressAllowedBy,
		},
		{
			name: "source reporter deny", reporter: goldmanepb.Reporter_Src, action: goldmanepb.Action_Deny,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS, wantVerdict: pb.Verdict_VERDICT_DROPPED,
			policies: (*pb.CalicoFlow).GetEgressDeniedBy,
		},
		{
			name: "destination reporter deny", reporter: goldmanepb.Reporter_Dst, action: goldmanepb.Action_Deny,
			wantDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_INGRESS, wantVerdict: pb.Verdict_VERDICT_DROPPED,
			policies: (*pb.CalicoFlow).GetIngressDeniedBy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flow := convertGoldmaneFlow(goldmaneFlow(tt.reporter, tt.action,
				policyHit(goldmanepb.PolicyKind_GlobalNetworkPolicy, "p", tt.action)))

			require.NotNil(t, flow)
			assert.Equal(t, tt.wantDirection, flow.GetTrafficDirection())
			assert.Equal(t, tt.wantVerdict, flow.GetVerdict())

			got := tt.policies(flow)
			require.Len(t, got, 1)
			assert.Equal(t, "p", got[0].GetName())
			assert.Equal(t, "GlobalNetworkPolicy", got[0].GetKind())

			total := len(flow.GetEgressAllowedBy()) + len(flow.GetIngressAllowedBy()) +
				len(flow.GetEgressDeniedBy()) + len(flow.GetIngressDeniedBy())
			assert.Equal(t, 1, total, "policy must only be set on one field")
		})
	}
}

func TestConvertGoldmaneFlow_PolicyTrace(t *testing.T) {
	trigger := policyHit(goldmanepb.PolicyKind_NetworkPolicy, "selects-cart", goldmanepb.Action_ActionUnspecified)
	endOfTier := &goldmanepb.PolicyHit{Kind: goldmanepb.PolicyKind_EndOfTier, Tier: "default", Action: goldmanepb.Action_Deny, Trigger: trigger}

	flow := convertGoldmaneFlow(goldmaneFlow(goldmanepb.Reporter_Dst, goldmanepb.Action_Deny,
		policyHit(goldmanepb.PolicyKind_GlobalNetworkPolicy, "security-pass", goldmanepb.Action_Pass),
		endOfTier))

	require.NotNil(t, flow)
	assert.Empty(t, flow.GetIngressAllowedBy(), "pass hits are skipped")
	require.Len(t, flow.GetIngressDeniedBy(), 1)
	assert.Equal(t, "selects-cart", flow.GetIngressDeniedBy()[0].GetName(), "end-of-tier deny is attributed to its trigger")
	assert.Equal(t, "NetworkPolicy", flow.GetIngressDeniedBy()[0].GetKind())
}

func TestConvertGoldmaneFlow_UnknownReporterHasNoPolicies(t *testing.T) {
	flow := convertGoldmaneFlow(goldmaneFlow(goldmanepb.Reporter_ReporterUnspecified, goldmanepb.Action_Allow,
		policyHit(goldmanepb.PolicyKind_NetworkPolicy, "p", goldmanepb.Action_Allow)))

	require.NotNil(t, flow)
	assert.Equal(t, pb.TrafficDirection_TRAFFIC_DIRECTION_TRAFFIC_DIRECTION_UNKNOWN_UNSPECIFIED, flow.GetTrafficDirection())
	assert.Empty(t, flow.GetEgressAllowedBy())
	assert.Empty(t, flow.GetIngressAllowedBy())
}

func TestConvertGoldmaneFlow_NoDestinationService(t *testing.T) {
	gf := goldmaneFlow(goldmanepb.Reporter_Src, goldmanepb.Action_Allow)
	gf.Key.DestServiceName = ""
	gf.Key.DestServiceNamespace = ""

	flow := convertGoldmaneFlow(gf)

	require.NotNil(t, flow)
	assert.Nil(t, flow.GetDestinationService())
}

func TestConvertGoldmaneFlow_NormalizesIPs(t *testing.T) {
	gf := goldmaneFlow(goldmanepb.Reporter_Src, goldmanepb.Action_Allow)
	gf.SourceIps = []string{"::ffff:10.0.0.1", "not-an-ip", "fd00::1"}

	flow := convertGoldmaneFlow(gf)

	require.NotNil(t, flow)
	assert.Equal(t, []string{"10.0.0.1", "fd00::1"}, flow.GetSourceIps())
}

func TestConvertGoldmaneFlow_Protocols(t *testing.T) {
	tests := []struct {
		proto string
		want  *pb.Layer4
	}{
		{proto: "udp", want: &pb.Layer4{Protocol: &pb.Layer4_Udp{Udp: &pb.UDP{DestinationPort: 8080}}}},
		{proto: "sctp", want: &pb.Layer4{Protocol: &pb.Layer4_Sctp{Sctp: &pb.SCTP{DestinationPort: 8080}}}},
		{proto: "icmp", want: &pb.Layer4{Protocol: &pb.Layer4_Icmpv4{Icmpv4: &pb.ICMPv4{}}}},
		{proto: "icmp6", want: &pb.Layer4{Protocol: &pb.Layer4_Icmpv6{Icmpv6: &pb.ICMPv6{}}}},
	}

	for _, tt := range tests {
		t.Run(tt.proto, func(t *testing.T) {
			gf := goldmaneFlow(goldmanepb.Reporter_Src, goldmanepb.Action_Allow)
			gf.Key.Proto = tt.proto

			flow := convertGoldmaneFlow(gf)

			require.NotNil(t, flow)
			assert.True(t, proto.Equal(tt.want, flow.GetLayer4()), "got %v", flow.GetLayer4())
		})
	}
}

func TestConvertGoldmaneFlow_Dropped(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*goldmanepb.Flow)
	}{
		{name: "nil key", mutate: func(f *goldmanepb.Flow) { f.Key = nil }},
		{name: "no source IPs (old Calico or IPs disabled)", mutate: func(f *goldmanepb.Flow) { f.SourceIps = nil }},
		{name: "no destination IPs", mutate: func(f *goldmanepb.Flow) { f.DestIps = nil }},
		{name: "only invalid IPs", mutate: func(f *goldmanepb.Flow) { f.SourceIps = []string{"bogus"} }},
		{name: "unsupported protocol", mutate: func(f *goldmanepb.Flow) { f.Key.Proto = "esp" }},
		{name: "numeric protocol", mutate: func(f *goldmanepb.Flow) { f.Key.Proto = "47" }},
		{name: "negative port", mutate: func(f *goldmanepb.Flow) { f.Key.DestPort = -1 }},
		{name: "port out of range", mutate: func(f *goldmanepb.Flow) { f.Key.DestPort = 70000 }},
		{name: "missing start time", mutate: func(f *goldmanepb.Flow) { f.StartTime = 0 }},
	}

	assert.Nil(t, convertGoldmaneFlow(nil))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gf := goldmaneFlow(goldmanepb.Reporter_Src, goldmanepb.Action_Allow)
			tt.mutate(gf)

			assert.Nil(t, convertGoldmaneFlow(gf))
		})
	}
}
