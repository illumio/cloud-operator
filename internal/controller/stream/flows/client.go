// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/controller/stream/flows/cache"
)

// Verify networkFlowsClient implements stream.StreamClient.
var _ stream.StreamClient = (*networkFlowsClient)(nil)

// KubernetesNetworkFlowsStream abstracts the SendKubernetesNetworkFlows gRPC stream.
type KubernetesNetworkFlowsStream interface {
	Send(req *pb.SendKubernetesNetworkFlowsRequest) error
	Recv() (*pb.SendKubernetesNetworkFlowsResponse, error)
}

// DefaultSendTimeout is how long a single Send on the network flows stream may
// block before the stream is canceled and reopened. A Send blocks when CloudSecure
// stops consuming the stream (HTTP/2 flow control) without closing the connection,
// which would otherwise stall the whole flow pipeline without any error.
const DefaultSendTimeout = 30 * time.Second

// networkFlowsClient implements stream.StreamClient for sending network flows to CloudSecure.
type networkFlowsClient struct {
	grpcStream KubernetesNetworkFlowsStream
	logger     *zap.Logger
	flowCache  *cache.FlowCache
	stats      *stream.Stats

	// cancelStream cancels the context the gRPC stream was opened with, which
	// aborts the stream and unblocks any in-progress Send.
	cancelStream context.CancelFunc
	sendTimeout  time.Duration

	// mutex guards closed and serializes Sends: gRPC does not allow concurrent
	// SendMsg calls on the same stream (flows and keepalives are sent from
	// different goroutines).
	mutex  sync.Mutex
	closed bool

	// sendStartedAt is the start time (UnixNano) of the in-progress Send, or 0.
	sendStartedAt     atomic.Int64
	flowsSentOnStream atomic.Uint64
}

// Run starts the flow cache and reads flows from it to send to CloudSecure.
func (c *networkFlowsClient) Run(ctx context.Context) (err error) {
	streamStart := time.Now()

	defer func() {
		c.logger.Info("Network flows stream ended",
			zap.Duration("stream_age", time.Since(streamStart)),
			zap.Uint64("flows_sent_on_stream", c.flowsSentOnStream.Load()),
			zap.Error(err),
		)
	}()

	// Start flow cache goroutine - handles eviction and moving flows to OutFlows channel.
	// The flow cache is shared across streams and is not safe for concurrent use, so
	// wait for it to stop before returning; the next stream starts its own.
	cacheCtx, cancelCache := context.WithCancel(ctx)
	cacheDone := make(chan struct{})

	defer func() {
		cancelCache()
		<-cacheDone
	}()

	go func() {
		defer close(cacheDone)

		if err := c.flowCache.Run(cacheCtx, c.logger); err != nil {
			c.logger.Debug("Flow cache stopped", zap.Error(err))
		}
	}()

	go c.watchBlockedSends(ctx, streamStart)

	// Read flows from cache and send to CloudSecure
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case flow, ok := <-c.flowCache.OutFlows:
			if !ok {
				// Flow cache channel closed; exit reader gracefully.
				return nil
			}

			err := c.sendNetworkFlowRequest(flow)
			if err != nil {
				return err
			}

			c.flowsSentOnStream.Add(1)
			c.stats.IncrementFlowsSentToClusterSync()
		}
	}
}

// watchBlockedSends cancels the stream when a Send has been blocked for longer
// than sendTimeout. The blocked Send then returns an error, Run returns, and the
// stream manager reopens the stream.
func (c *networkFlowsClient) watchBlockedSends(ctx context.Context, streamStart time.Time) {
	ticker := time.NewTicker(c.sendTimeout / 3)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			blockedFor := sinceUnixNano(c.sendStartedAt.Load())
			if blockedFor < c.sendTimeout {
				continue
			}

			c.logger.Error("Send on network flows stream is blocked; CloudSecure is not consuming the stream, reopening it",
				zap.Duration("send_blocked_for", blockedFor),
				zap.Duration("stream_age", time.Since(streamStart)),
				zap.Uint64("flows_sent_on_stream", c.flowsSentOnStream.Load()),
				zap.Int("flow_cache_out_queue_len", len(c.flowCache.OutFlows)),
			)
			c.cancelStream()

			return
		}
	}
}

// sinceUnixNano returns the time elapsed since the given UnixNano timestamp,
// or 0 if the timestamp is 0.
func sinceUnixNano(startedAt int64) time.Duration {
	if startedAt == 0 {
		return 0
	}

	return time.Since(time.Unix(0, startedAt))
}

// send sends a request on the gRPC stream. Sends are serialized, and the start
// time of the in-progress Send is recorded for watchBlockedSends.
func (c *networkFlowsClient) send(request *pb.SendKubernetesNetworkFlowsRequest) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.closed {
		return errors.New("stream closed")
	}

	c.sendStartedAt.Store(time.Now().UnixNano())
	defer c.sendStartedAt.Store(0)

	return c.grpcStream.Send(request)
}

// sendNetworkFlowRequest sends a network flow to the networkFlowsStream.
func (c *networkFlowsClient) sendNetworkFlowRequest(flow any) error {
	var request *pb.SendKubernetesNetworkFlowsRequest

	switch f := flow.(type) {
	case *pb.FiveTupleFlow:
		request = &pb.SendKubernetesNetworkFlowsRequest{
			Request: &pb.SendKubernetesNetworkFlowsRequest_FiveTupleFlow{
				FiveTupleFlow: f,
			},
		}
	case *pb.CiliumFlow:
		request = &pb.SendKubernetesNetworkFlowsRequest{
			Request: &pb.SendKubernetesNetworkFlowsRequest_CiliumFlow{
				CiliumFlow: f,
			},
		}
	default:
		return fmt.Errorf("unsupported flow type: %T", flow)
	}

	if err := c.send(request); err != nil {
		c.logger.Error("Failed to send network flow", zap.Error(err))

		return err
	}

	return nil
}

// SendKeepalive sends a keepalive message on the network flows stream.
func (c *networkFlowsClient) SendKeepalive(_ context.Context) error {
	err := c.send(&pb.SendKubernetesNetworkFlowsRequest{
		Request: &pb.SendKubernetesNetworkFlowsRequest_Keepalive{
			Keepalive: &pb.Keepalive{},
		},
	})
	if err != nil {
		c.logger.Error("Failed to send keepalive on network flows stream", zap.Error(err))

		return err
	}

	return nil
}

// Close cancels the stream and marks the client as closed.
func (c *networkFlowsClient) Close() error {
	// Cancel first so that a blocked Send returns and releases the mutex.
	c.cancelStream()

	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.closed = true

	return nil
}
