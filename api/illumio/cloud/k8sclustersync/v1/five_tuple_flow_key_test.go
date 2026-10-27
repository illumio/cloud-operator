// Copyright 2026 Illumio, Inc. All Rights Reserved.

package k8sclustersyncv1

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func testFiveTupleFlow(direction TrafficDirection, verdict Verdict) *FiveTupleFlow {
	return &FiveTupleFlow{
		Layer3: &IP{Source: "10.244.163.79", Destination: "10.244.163.65", IpVersion: IPVersion_IP_VERSION_IPV4},
		Layer4: &Layer4{Protocol: &Layer4_Udp{Udp: &UDP{
			SourcePort:      59106,
			DestinationPort: 53,
		}}},
		Ts:               &FiveTupleFlow_Timestamp{Timestamp: timestamppb.New(time.Unix(1793048988, 0))},
		TrafficDirection: direction,
		Verdict:          verdict,
	}
}

func TestFiveTupleFlowKey(t *testing.T) {
	egressAllowed := testFiveTupleFlow(TrafficDirection_TRAFFIC_DIRECTION_EGRESS, Verdict_VERDICT_FORWARDED)

	assert.Equal(t, FiveTupleFlowKey{
		SourceIP:         "10.244.163.79",
		DestinationIP:    "10.244.163.65",
		SourcePort:       59106,
		DestinationPort:  53,
		Protocol:         "UDP",
		TrafficDirection: TrafficDirection_TRAFFIC_DIRECTION_EGRESS,
		Verdict:          Verdict_VERDICT_FORWARDED,
	}, egressAllowed.Key())

	tests := map[string]struct {
		other    *FiveTupleFlow
		sameAsEq bool
	}{
		"same flow with different policies has the same key": {
			other: func() *FiveTupleFlow {
				f := testFiveTupleFlow(TrafficDirection_TRAFFIC_DIRECTION_EGRESS, Verdict_VERDICT_FORWARDED)
				f.EgressAllowedBy = []*Policy{{Name: "allow-dns"}}

				return f
			}(),
			sameAsEq: true,
		},
		"the other reporting side has a different key": {
			other: testFiveTupleFlow(TrafficDirection_TRAFFIC_DIRECTION_INGRESS, Verdict_VERDICT_FORWARDED),
		},
		"a denied flow has a different key from an allowed one": {
			other: testFiveTupleFlow(TrafficDirection_TRAFFIC_DIRECTION_EGRESS, Verdict_VERDICT_DROPPED),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if tt.sameAsEq {
				assert.Equal(t, egressAllowed.Key(), tt.other.Key())
			} else {
				assert.NotEqual(t, egressAllowed.Key(), tt.other.Key())
			}
		})
	}
}

// Collectors that report no verdict or direction keep keying on the five-tuple.
func TestFiveTupleFlowKeyWithoutVerdictOrDirection(t *testing.T) {
	flow := testFiveTupleFlow(TrafficDirection_TRAFFIC_DIRECTION_TRAFFIC_DIRECTION_UNKNOWN_UNSPECIFIED, Verdict_VERDICT_UNKNOWN_UNSPECIFIED)

	assert.Equal(t, FiveTupleFlowKey{
		SourceIP:        "10.244.163.79",
		DestinationIP:   "10.244.163.65",
		SourcePort:      59106,
		DestinationPort: 53,
		Protocol:        "UDP",
	}, flow.Key())
}

func TestFiveTupleFlowKeyNil(t *testing.T) {
	var flow *FiveTupleFlow

	assert.Nil(t, flow.Key())
}
