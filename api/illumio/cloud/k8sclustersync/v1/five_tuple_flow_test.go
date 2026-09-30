package k8sclustersyncv1

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFiveTupleFlowKey_Verdict(t *testing.T) {
	newFlow := func(verdict Verdict) *FiveTupleFlow {
		return &FiveTupleFlow{
			Layer3:  &IP{Source: "10.0.1.28", Destination: "10.0.1.132", IpVersion: IPVersion_IP_VERSION_IPV4},
			Layer4:  &Layer4{Protocol: &Layer4_Tcp{Tcp: &TCP{SourcePort: 55484, DestinationPort: 80}}},
			Verdict: verdict,
		}
	}

	accept, acceptAgain := newFlow(Verdict_VERDICT_FORWARDED), newFlow(Verdict_VERDICT_FORWARDED)
	deny := newFlow(Verdict_VERDICT_DROPPED)
	unset, unsetAgain := newFlow(Verdict_VERDICT_UNKNOWN_UNSPECIFIED), newFlow(Verdict_VERDICT_UNKNOWN_UNSPECIFIED)

	// The flow cache dedupes on Key() (first wins), so an ACCEPT must not hide a
	// DENY for the same 5-tuple.
	assert.NotEqual(t, accept.Key(), deny.Key())

	assert.Equal(t, accept.Key(), acceptAgain.Key())
	// Collectors that don't report a verdict (OVN-K, Falco) keep deduping on the 5-tuple.
	assert.Equal(t, unset.Key(), unsetAgain.Key())
}

func TestFiveTupleFlowKey_DirectionNotInKey(t *testing.T) {
	newFlow := func(direction TrafficDirection) *FiveTupleFlow {
		return &FiveTupleFlow{
			Layer3:           &IP{Source: "10.0.1.28", Destination: "10.0.1.132", IpVersion: IPVersion_IP_VERSION_IPV4},
			Layer4:           &Layer4{Protocol: &Layer4_Tcp{Tcp: &TCP{SourcePort: 55484, DestinationPort: 80}}},
			Verdict:          Verdict_VERDICT_FORWARDED,
			TrafficDirection: direction,
		}
	}

	// The AWS agent logs one connection twice when both pods are selected by a
	// policy: egress from the source pod and ingress at the destination pod. These
	// share a key, so only one of them is sent.
	egress := newFlow(TrafficDirection_TRAFFIC_DIRECTION_EGRESS)
	ingress := newFlow(TrafficDirection_TRAFFIC_DIRECTION_INGRESS)

	assert.Equal(t, egress.Key(), ingress.Key())
}

func TestFiveTupleFlowKey_Protocols(t *testing.T) {
	tests := []struct {
		name   string
		layer4 *Layer4
		want   FiveTupleFlowKey
	}{
		{
			name:   "UDP",
			layer4: &Layer4{Protocol: &Layer4_Udp{Udp: &UDP{SourcePort: 53000, DestinationPort: 53}}},
			want:   FiveTupleFlowKey{SourcePort: 53000, DestinationPort: 53, Protocol: "UDP"},
		},
		{
			name:   "SCTP",
			layer4: &Layer4{Protocol: &Layer4_Sctp{Sctp: &SCTP{SourcePort: 2905, DestinationPort: 2906}}},
			want:   FiveTupleFlowKey{SourcePort: 2905, DestinationPort: 2906, Protocol: "SCTP"},
		},
		{
			name:   "ICMPv4",
			layer4: &Layer4{Protocol: &Layer4_Icmpv4{Icmpv4: &ICMPv4{}}},
			want:   FiveTupleFlowKey{Protocol: "ICMPv4"},
		},
		{
			name:   "ICMPv6",
			layer4: &Layer4{Protocol: &Layer4_Icmpv6{Icmpv6: &ICMPv6{}}},
			want:   FiveTupleFlowKey{Protocol: "ICMPv6"},
		},
		{
			name: "no layer4",
			want: FiveTupleFlowKey{Protocol: "UNKNOWN"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flow := &FiveTupleFlow{
				Layer3:  &IP{Source: "10.0.0.1", Destination: "10.0.0.2"},
				Layer4:  tt.layer4,
				Verdict: Verdict_VERDICT_DROPPED,
			}

			tt.want.SourceIP = "10.0.0.1"
			tt.want.DestinationIP = "10.0.0.2"
			tt.want.Verdict = Verdict_VERDICT_DROPPED
			assert.Equal(t, tt.want, flow.Key())
		})
	}
}

func TestFiveTupleFlowKey_NilFlow(t *testing.T) {
	var flow *FiveTupleFlow

	assert.Nil(t, flow.Key())
}
