// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// blockingNetworkFlowsStream is a network flows stream whose Send does not return until
// release is closed, so a test controls how long a send is in flight.
type blockingNetworkFlowsStream struct {
	// entered receives a value each time a Send starts.
	entered chan struct{}
	// release is closed to let every Send return.
	release chan struct{}
}

func (s *blockingNetworkFlowsStream) Send(_ *pb.SendKubernetesNetworkFlowsRequest) error {
	s.entered <- struct{}{}

	<-s.release

	return nil
}

func (s *blockingNetworkFlowsStream) Recv() (*pb.SendKubernetesNetworkFlowsResponse, error) {
	return nil, io.EOF
}

// A gRPC stream allows only one Send at a time. The keepalive goroutine and the
// goroutine that sends flows share the stream, and two concurrent sends can
// leave one of them waiting forever for write quota.
func TestNetworkFlowsClient_KeepaliveDoesNotOverlapFlowSend(t *testing.T) {
	grpcStream := &blockingNetworkFlowsStream{
		entered: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	client := &networkFlowsClient{grpcStream: grpcStream, logger: zap.NewNop(), stats: stream.NewStats()}

	var wg sync.WaitGroup

	wg.Go(func() {
		assert.NoError(t, client.sendNetworkFlowRequest(&pb.FiveTupleFlow{}))
	})

	select {
	case <-grpcStream.entered:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the flow send did not start")
	}

	// The flow send stays in flight until release is closed. A keepalive that
	// enters Send before then overlaps it.
	wg.Go(func() {
		assert.NoError(t, client.SendKeepalive(context.Background()))
	})

	overlapped := false

	select {
	case <-grpcStream.entered:
		overlapped = true
	case <-time.After(100 * time.Millisecond):
	}

	close(grpcStream.release)
	wg.Wait()

	assert.False(t, overlapped, "a keepalive was sent on the stream while a flow send was in flight")
}
