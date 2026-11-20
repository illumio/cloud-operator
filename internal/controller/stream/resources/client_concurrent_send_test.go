// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

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
)

// overlapDetectingResourcesStream is a resources stream whose Send records
// whether another Send was running at the same time.
type overlapDetectingResourcesStream struct {
	inFlight   atomic.Int32
	overlapped atomic.Bool
	// entered receives a value each time a Send starts.
	entered chan struct{}
	// sendDuration is how long each Send stays in flight.
	sendDuration time.Duration
}

func (s *overlapDetectingResourcesStream) Send(_ *pb.SendKubernetesResourcesRequest) error {
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

func (s *overlapDetectingResourcesStream) Recv() (*pb.SendKubernetesResourcesResponse, error) {
	return nil, nil
}

// A gRPC stream allows only one Send at a time. The keepalive goroutine and the
// goroutine that sends resources share the stream, and two concurrent sends can
// leave one of them waiting forever for write quota, which stops the resource
// snapshot until the liveness probe restarts the operator.
func TestResourcesClient_KeepaliveDoesNotOverlapResourceSend(t *testing.T) {
	grpcStream := &overlapDetectingResourcesStream{
		entered:      make(chan struct{}, 2),
		sendDuration: 20 * time.Millisecond,
	}
	client := &resourcesClient{grpcStream: grpcStream, logger: zap.NewNop()}

	var wg sync.WaitGroup

	wg.Go(func() {
		assert.NoError(t, client.SendObjectData(zap.NewNop(), &pb.KubernetesObjectData{Name: "pod-0"}))
	})

	// Send the keepalive while the resource send is in flight.
	select {
	case <-grpcStream.entered:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the resource send did not start")
	}

	wg.Go(func() {
		assert.NoError(t, client.SendKeepalive(context.Background()))
	})

	wg.Wait()

	assert.False(t, grpcStream.overlapped.Load(), "a keepalive and a resource were sent on the stream at the same time")
}
