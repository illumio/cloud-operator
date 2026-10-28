// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"net"
	"strconv"
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
// TCP syslog collector on an ephemeral port, dials it like the LogCollector
// would, writes newline-framed LogCollector records, and asserts they are
// parsed and cached.
func TestRunSyslogCollectorEndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Grab a free port, then hand its number to the collector.
	var listenConfig net.ListenConfig

	probe, err := listenConfig.Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	probeAddr, ok := probe.Addr().(*net.TCPAddr)
	require.True(t, ok)

	port := strconv.Itoa(probeAddr.Port)

	require.NoError(t, probe.Close())

	sink := &captureSink{}
	c := NewCalicoCollector(zap.NewNop(), port, CalicoSyslogTLS{}, sink, nil)

	var wg sync.WaitGroup

	wg.Go(func() {
		_ = c.RunSyslogCollector(ctx)
	})

	// Wait for the listener to come up.
	conn := dialWithRetry(ctx, t, "127.0.0.1:"+port)

	// A TCP flow reported by the source and a UDP flow reported by the destination.
	records := "" +
		`<14>Oct 26 21:10:20 calico-ent tigera_secure: {"start_time":1793049004,"source_ip":"10.244.120.78","source_port":48710,"dest_ip":"10.244.120.74","dest_port":8080,"proto":"tcp","action":"allow","reporter":"src","policies":{"enforced_policies":["0|default|np:shop/default.allow-web|allow|0"]}}` + "\n" +
		`<14>Oct 26 21:10:20 calico-ent tigera_secure: {"start_time":1793049004,"source_ip":"10.244.120.78","source_port":33001,"dest_ip":"10.96.0.10","dest_port":53,"proto":"udp","action":"deny","reporter":"dst","policies":{"enforced_policies":["1|security|gnp:security.default-deny|deny|0"]}}` + "\n"

	_, err = conn.Write([]byte(records))
	require.NoError(t, err)

	// Poll until both flows land or we time out.
	require.Eventually(t, func() bool {
		return sink.count() == 2
	}, 2*time.Second, 10*time.Millisecond, "expected 2 cached flows")

	require.NoError(t, conn.Close())
	cancel()
	wg.Wait()

	first, ok := sink.flows[0].(*pb.FiveTupleFlow)
	require.True(t, ok)
	assert.Equal(t, "10.244.120.78", first.GetLayer3().GetSource())
	assert.Equal(t, "10.244.120.74", first.GetLayer3().GetDestination())
	assert.Equal(t, pb.IPVersion_IP_VERSION_IPV4, first.GetLayer3().GetIpVersion())
	assert.Equal(t, uint32(48710), first.GetLayer4().GetTcp().GetSourcePort())
	assert.Equal(t, uint32(8080), first.GetLayer4().GetTcp().GetDestinationPort())

	second, ok := sink.flows[1].(*pb.FiveTupleFlow)
	require.True(t, ok)
	assert.Equal(t, uint32(33001), second.GetLayer4().GetUdp().GetSourcePort())
	assert.Equal(t, uint32(53), second.GetLayer4().GetUdp().GetDestinationPort())

	assert.Equal(t, 2, sink.received)
}

func dialWithRetry(ctx context.Context, t *testing.T, addr string) net.Conn {
	t.Helper()

	var (
		conn   net.Conn
		dialer net.Dialer
	)

	require.Eventually(t, func() bool {
		c, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return false
		}

		conn = c

		return true
	}, 2*time.Second, 10*time.Millisecond, "collector never started listening")

	return conn
}
