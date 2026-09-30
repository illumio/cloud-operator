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
