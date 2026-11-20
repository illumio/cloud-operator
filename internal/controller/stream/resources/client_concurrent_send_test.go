// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// blockingResourcesStream is a resources stream whose Send does not return until
// release is closed, so a test controls how long a send is in flight.
type blockingResourcesStream struct {
	// entered receives a value each time a Send starts.
	entered chan struct{}
	// release is closed to let every Send return.
	release chan struct{}
}

func (s *blockingResourcesStream) Send(_ *pb.SendKubernetesResourcesRequest) error {
	s.entered <- struct{}{}

	<-s.release

	return nil
}

func (s *blockingResourcesStream) Recv() (*pb.SendKubernetesResourcesResponse, error) {
	return nil, nil
}

// A gRPC stream allows only one Send at a time. The keepalive goroutine and the
// goroutine that sends resources share the stream, and two concurrent sends can
// leave one of them waiting forever for write quota, which stops the resource
// snapshot until the liveness probe restarts the operator.
func TestResourcesClient_KeepaliveDoesNotOverlapResourceSend(t *testing.T) {
	grpcStream := &blockingResourcesStream{
		entered: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	client := &resourcesClient{grpcStream: grpcStream, logger: zap.NewNop()}

	var wg sync.WaitGroup

	wg.Go(func() {
		assert.NoError(t, client.SendObjectData(zap.NewNop(), &pb.KubernetesObjectData{Name: "pod-0"}))
	})

	select {
	case <-grpcStream.entered:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the resource send did not start")
	}

	// The resource send stays in flight until release is closed. A keepalive
	// that enters Send before then overlaps it.
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

	assert.False(t, overlapped, "a keepalive was sent on the stream while a resource send was in flight")
}
