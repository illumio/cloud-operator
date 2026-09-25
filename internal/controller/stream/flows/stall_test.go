// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/cache"
)

// stallingFlowsServer accepts network flow streams. The first stream is held
// open but never read, so HTTP/2 flow control eventually blocks the client's
// Send (this mimics nginx accepting a stream whose upstream is not consuming).
// Subsequent streams are drained normally.
type stallingFlowsServer struct {
	pb.UnimplementedKubernetesInfoServiceServer

	streamsOpened atomic.Int32
	flowsRead     atomic.Int64
}

func (s *stallingFlowsServer) SendKubernetesNetworkFlows(
	srv grpc.BidiStreamingServer[pb.SendKubernetesNetworkFlowsRequest, pb.SendKubernetesNetworkFlowsResponse],
) error {
	if s.streamsOpened.Add(1) == 1 {
		<-srv.Context().Done()

		return srv.Context().Err()
	}

	for {
		req, err := srv.Recv()
		if err != nil {
			return err
		}

		if req.GetCiliumFlow() != nil {
			s.flowsRead.Add(1)
		}
	}
}

func newReproCiliumFlow(i int) *pb.CiliumFlow {
	return &pb.CiliumFlow{
		Time: timestamppb.Now(),
		Layer3: &pb.IP{
			Source:      "10.0.0.1",
			Destination: "10.0.0.2",
		},
		Layer4: &pb.Layer4{Protocol: &pb.Layer4_Tcp{Tcp: &pb.TCP{
			SourcePort:      uint32(i%60000 + 1024), //nolint:gosec
			DestinationPort: uint32(i / 60000),      //nolint:gosec
		}}},
		SourceEndpoint: &pb.Endpoint{
			Namespace: "default",
			PodName:   "client-pod-with-a-reasonably-long-name",
			Labels:    []string{"k8s:app=client", "k8s:team=payments", "k8s:env=prod"},
		},
	}
}

// TestFlowStreamRecoversWhenServerStopsReading reproduces the customer
// incident where flows_received dropped to 0 after an upstream 502/EOF: the
// flows stream reconnected but the server side never consumed it. A healthy
// operator must detect the stuck stream and reconnect.
func TestFlowStreamRecoversWhenServerStopsReading(t *testing.T) {
	logger := zaptest.NewLogger(t, zaptest.Level(zap.InfoLevel))

	lis := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	fake := &stallingFlowsServer{}
	pb.RegisterKubernetesInfoServiceServer(server, fake)

	go func() { _ = server.Serve(lis) }()

	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	stats := stream.NewStats()
	flowCache := cache.NewFlowCache(100*time.Millisecond, 1000, make(chan pb.Flow, 100))
	sink := NewFlowSinkAdapter(flowCache, stats)

	var flowsReceived atomic.Int64

	// Simulated Cilium collector: Hubble Relay delivers flows continuously.
	go func() {
		for i := 0; ctx.Err() == nil; i++ {
			if err := sink.CacheFlow(ctx, newReproCiliumFlow(i)); err != nil {
				return
			}

			sink.IncrementFlowsReceived()
			flowsReceived.Add(1)
		}
	}()

	done := make(chan struct{})
	go stream.ManageStream(ctx, logger, conn,
		&NetworkFlowsFactory{Logger: logger, FlowCache: flowCache, Stats: stats, SendTimeout: time.Second},
		time.Second, // keepalive period
		stream.SuccessPeriods{Auth: time.Hour, Connect: time.Minute},
		done,
	)

	// Stop the stream manager before the test ends so it doesn't log afterwards.
	t.Cleanup(func() {
		cancel()
		<-done
	})

	// Wait until the collector is wedged behind the blocked stream.
	var last int64

	require.Eventually(t, func() bool {
		cur := flowsReceived.Load()
		stalled := cur > 0 && cur == last
		last = cur

		return stalled
	}, 10*time.Second, 500*time.Millisecond, "collector never stalled; repro setup is wrong")

	t.Logf("pipeline stalled: flows_received=%d streams_opened=%d", flowsReceived.Load(), fake.streamsOpened.Load())

	// Expected behavior: the operator notices the stuck stream, reconnects,
	// and flows resume. Before the send timeout, this never happened.
	require.Eventually(t, func() bool {
		return fake.streamsOpened.Load() >= 2 && fake.flowsRead.Load() > 0
	}, 20*time.Second, 200*time.Millisecond,
		"flows stream stayed stuck: streams_opened=%d flows_received=%d (operator never recovered)",
		fake.streamsOpened.Load(), flowsReceived.Load())
}
