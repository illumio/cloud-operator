// Copyright 2026 Illumio, Inc. All Rights Reserved.

package k8sclustersyncv1

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func newTestCalicoFlow(direction TrafficDirection, sourceIPs ...string) *CalicoFlow {
	return &CalicoFlow{
		TrafficDirection:    direction,
		Verdict:             Verdict_VERDICT_FORWARDED,
		SourceIps:           sourceIPs,
		DestinationIps:      []string{"10.0.2.40"},
		Layer4:              &Layer4{Protocol: &Layer4_Tcp{Tcp: &TCP{DestinationPort: 8080}}},
		SourceEndpoint:      &CalicoEndpoint{Namespace: "shop", AggregatedName: "frontend-*"},
		DestinationEndpoint: &CalicoEndpoint{Namespace: "shop", AggregatedName: "cart-*"},
	}
}

func TestCalicoFlowKey_IPOrderDoesNotMatter(t *testing.T) {
	a := newTestCalicoFlow(TrafficDirection_TRAFFIC_DIRECTION_EGRESS, "10.0.1.13", "10.0.1.12")
	b := newTestCalicoFlow(TrafficDirection_TRAFFIC_DIRECTION_EGRESS, "10.0.1.12", "10.0.1.13")

	assert.Equal(t, a.Key(), b.Key())
	assert.Equal(t, []string{"10.0.1.13", "10.0.1.12"}, a.GetSourceIps(), "Key must not reorder the flow's IPs")
}

func TestCalicoFlowKey_DirectionIsKeyed(t *testing.T) {
	egress := newTestCalicoFlow(TrafficDirection_TRAFFIC_DIRECTION_EGRESS, "10.0.1.12")
	ingress := newTestCalicoFlow(TrafficDirection_TRAFFIC_DIRECTION_INGRESS, "10.0.1.12")

	assert.NotEqual(t, egress.Key(), ingress.Key())
}

func TestCalicoFlowKey_VerdictIsKeyed(t *testing.T) {
	allowed := newTestCalicoFlow(TrafficDirection_TRAFFIC_DIRECTION_EGRESS, "10.0.1.12")
	denied := newTestCalicoFlow(TrafficDirection_TRAFFIC_DIRECTION_EGRESS, "10.0.1.12")
	denied.Verdict = Verdict_VERDICT_DROPPED

	assert.NotEqual(t, allowed.Key(), denied.Key())
}

func TestCalicoFlowKey_Nil(t *testing.T) {
	var flow *CalicoFlow

	assert.Nil(t, flow.Key())
}
