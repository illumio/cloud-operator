// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
	"errors"
	"fmt"
	"sync"

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

// networkFlowsClient implements stream.StreamClient for sending network flows to CloudSecure.
type networkFlowsClient struct {
	grpcStream KubernetesNetworkFlowsStream
	logger     *zap.Logger
	flowCache  *cache.FlowCache
	stats      *stream.Stats

	mutex  sync.RWMutex
	closed bool
}

// Run starts the flow cache and reads flows from it to send to CloudSecure.
func (c *networkFlowsClient) Run(ctx context.Context) error {
	// Start flow cache goroutine - handles eviction and moving flows to OutFlows channel
	go func() {
		if err := c.flowCache.Run(ctx, c.logger); err != nil {
			c.logger.Debug("Flow cache stopped", zap.Error(err))
		}
	}()

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

			c.stats.IncrementFlowsSentToClusterSync()
		}
	}
}

// sendNetworkFlowRequest sends a network flow to the networkFlowsStream.
func (c *networkFlowsClient) sendNetworkFlowRequest(flow interface{}) error {
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

	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if c.closed {
		return errors.New("stream closed")
	}

	if err := c.grpcStream.Send(request); err != nil {
		c.logger.Error("Failed to send network flow", zap.Error(err))

		return err
	}

	return nil
}

// SendKeepalive sends a keepalive message on the network flows stream.
func (c *networkFlowsClient) SendKeepalive(_ context.Context) error {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if c.closed {
		return errors.New("stream closed")
	}

	err := c.grpcStream.Send(&pb.SendKubernetesNetworkFlowsRequest{
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

// Close marks the client as closed.
func (c *networkFlowsClient) Close() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.closed = true

	return nil
}
