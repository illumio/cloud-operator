// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// overlapDetectingNetworkFlowsStream is a network flows stream whose Send
// records whether another Send was running at the same time.
type overlapDetectingNetworkFlowsStream struct {
	inFlight   atomic.Int32
	overlapped atomic.Bool
	// entered receives a value each time a Send starts.
	entered chan struct{}
	// sendDuration is how long each Send stays in flight.
	sendDuration time.Duration
}

func (s *overlapDetectingNetworkFlowsStream) Send(_ *pb.SendKubernetesNetworkFlowsRequest) error {
	if s.inFlight.Add(1) > 1 {
		s.overlapped.Store(true)
	}
	defer s.inFlight.Add(-1)

	select {
	case s.entered <- struct{}{}:
	default:
	}

	time.Sleep(s.sendDuration)

	return nil
}

func (s *overlapDetectingNetworkFlowsStream) Recv() (*pb.SendKubernetesNetworkFlowsResponse, error) {
	return nil, nil
}

// A gRPC stream allows only one Send at a time. The keepalive goroutine and the
// goroutine that sends flows share the stream, and two concurrent sends can
// leave one of them waiting forever for write quota.
func TestNetworkFlowsClient_KeepaliveDoesNotOverlapFlowSend(t *testing.T) {
	grpcStream := &overlapDetectingNetworkFlowsStream{
		entered:      make(chan struct{}, 2),
		sendDuration: 20 * time.Millisecond,
	}
	client := &networkFlowsClient{grpcStream: grpcStream, logger: zap.NewNop(), stats: stream.NewStats()}

	var wg sync.WaitGroup

	wg.Go(func() {
		assert.NoError(t, client.sendNetworkFlowRequest(&pb.FiveTupleFlow{}))
	})

	// Send the keepalive while the flow send is in flight.
	select {
	case <-grpcStream.entered:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the flow send did not start")
	}

	wg.Go(func() {
		assert.NoError(t, client.SendKeepalive(context.Background()))
	})

	wg.Wait()

	assert.False(t, grpcStream.overlapped.Load(), "a keepalive and a flow were sent on the stream at the same time")
}
