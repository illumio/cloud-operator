// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// captureSink records flows cached by the collector for assertions.
type captureSink struct {
	mu       sync.Mutex
	flows    []pb.Flow
	received int
}

func (s *captureSink) CacheFlow(_ context.Context, flow pb.Flow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flows = append(s.flows, flow)

	return nil
}

func (s *captureSink) IncrementFlowsReceived() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received++
}

func (s *captureSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.flows)
}

// TestRunSyslogCollectorEndToEnd exercises the full listener path: it starts the
// TCP syslog collector on an ephemeral port, dials it like Felix would, writes
// real captured newline-framed records, and asserts they are parsed and cached.
func TestRunSyslogCollectorEndToEnd(t *testing.T) {
	// Grab a free port, then hand its number to the collector.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := fmt.Sprintf("%d", probe.Addr().(*net.TCPAddr).Port)
	require.NoError(t, probe.Close())

	sink := &captureSink{}
	c := NewCalicoCollector(zap.NewNop(), port, sink)

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = c.RunSyslogCollector(ctx)
	}()

	// Wait for the listener to come up.
	conn := dialWithRetry(t, "127.0.0.1:"+port)

	// Two real captured records (TCP allow + UDP deny), newline-framed per RFC 6587.
	records := "" +
		`<14>2026-06-29T22:45:59Z minikube calico-felix[51097]: INFO <nil> <nil>:  action="allow" dstIP="10.244.120.74" dstPort="8080" proto="6" srcIP="10.244.120.78" srcPort="48710" updateType=report` + "\n" +
		`<14>2026-06-29T22:45:59Z minikube calico-felix[51097]: INFO <nil> <nil>:  action="deny" dstIP="10.96.0.10" dstPort="53" proto="17" srcIP="10.244.120.78" srcPort="33001" updateType=report` + "\n"

	_, err = conn.Write([]byte(records))
	require.NoError(t, err)

	// Poll until both flows land or we time out.
	require.Eventually(t, func() bool {
		return sink.count() == 2
	}, 2*time.Second, 10*time.Millisecond, "expected 2 cached flows")

	require.NoError(t, conn.Close())
	cancel()
	wg.Wait()

	// Verify the five-tuple of the first (TCP) flow survived the round trip.
	first, ok := sink.flows[0].(*pb.FiveTupleFlow)
	require.True(t, ok)
	assert.Equal(t, "10.244.120.78", first.Layer3.Source)
	assert.Equal(t, "10.244.120.74", first.Layer3.Destination)
	assert.Equal(t, pb.IPVersion_IP_VERSION_IPV4, first.Layer3.IpVersion)
	assert.Equal(t, uint32(48710), first.Layer4.GetTcp().SourcePort)
	assert.Equal(t, uint32(8080), first.Layer4.GetTcp().DestinationPort)

	// Second flow is UDP.
	second := sink.flows[1].(*pb.FiveTupleFlow)
	assert.Equal(t, uint32(33001), second.Layer4.GetUdp().SourcePort)
	assert.Equal(t, uint32(53), second.Layer4.GetUdp().DestinationPort)

	assert.Equal(t, 2, sink.received)
}

func dialWithRetry(t *testing.T, addr string) net.Conn {
	t.Helper()

	var conn net.Conn

	require.Eventually(t, func() bool {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return false
		}
		conn = c

		return true
	}, 2*time.Second, 10*time.Millisecond, "collector never started listening")

	return conn
}
