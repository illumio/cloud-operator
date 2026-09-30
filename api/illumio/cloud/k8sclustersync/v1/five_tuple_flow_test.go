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

	// The flow cache dedupes on Key() (first wins), so an ACCEPT must not hide a
	// DENY for the same 5-tuple.
	assert.NotEqual(t, newFlow(Verdict_VERDICT_FORWARDED).Key(), newFlow(Verdict_VERDICT_DROPPED).Key())

	assert.Equal(t, newFlow(Verdict_VERDICT_FORWARDED).Key(), newFlow(Verdict_VERDICT_FORWARDED).Key())
	// Collectors that don't report a verdict (OVN-K, Falco) keep deduping on the 5-tuple.
	assert.Equal(t, newFlow(Verdict_VERDICT_UNKNOWN_UNSPECIFIED).Key(), newFlow(Verdict_VERDICT_UNKNOWN_UNSPECIFIED).Key())
}
