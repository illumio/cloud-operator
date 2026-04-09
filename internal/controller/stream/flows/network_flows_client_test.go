// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// mockNetworkFlowsStream mocks the stream.KubernetesNetworkFlowsStream interface.
type mockNetworkFlowsStream struct {
	mock.Mock
}

func (m *mockNetworkFlowsStream) Send(req *pb.SendKubernetesNetworkFlowsRequest) error {
	args := m.Called(req)

	return args.Error(0)
}

func (m *mockNetworkFlowsStream) Recv() (*pb.SendKubernetesNetworkFlowsResponse, error) {
	args := m.Called()
	if resp, ok := args.Get(0).(*pb.SendKubernetesNetworkFlowsResponse); ok {
		return resp, args.Error(1)
	}

	return nil, args.Error(1)
}

// NetworkFlowsClientTestSuite tests the networkFlowsClient.
type NetworkFlowsClientTestSuite struct {
	suite.Suite

	mockStream *mockNetworkFlowsStream
	flowCache  *stream.FlowCache
	stats      *stream.Stats
	logger     *zap.Logger
	client     *networkFlowsClient
	outFlows   chan pb.Flow
}

func TestNetworkFlowsClientTestSuite(t *testing.T) {
	suite.Run(t, new(NetworkFlowsClientTestSuite))
}

func (s *NetworkFlowsClientTestSuite) SetupTest() {
	s.mockStream = &mockNetworkFlowsStream{}
	s.logger = zap.NewNop()
	s.stats = stream.NewStats()
	s.outFlows = make(chan pb.Flow, 10)
	s.flowCache = stream.NewFlowCache(10*time.Second, 100, s.outFlows)
	s.client = &networkFlowsClient{
		grpcStream: s.mockStream,
		logger:     s.logger,
		flowCache:  s.flowCache,
		stats:      s.stats,
	}
}

func (s *NetworkFlowsClientTestSuite) TearDownTest() {
	// No explicit Close() needed - channels are garbage collected when test ends.
	// Calling Close() here would race with flowCache.Run() goroutine.
}

func (s *NetworkFlowsClientTestSuite) TestRun_ContextCanceled() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := s.client.Run(ctx)

	s.Require().ErrorIs(err, context.Canceled)
}

func (s *NetworkFlowsClientTestSuite) TestRun_ChannelClosed() {
	// Close the OutFlows channel
	close(s.outFlows)

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
}

func (s *NetworkFlowsClientTestSuite) TestRun_SendFiveTupleFlow() {
	flow := &pb.FiveTupleFlow{
		Layer3: &pb.IP{
			Source:      "10.0.0.1",
			Destination: "10.0.0.2",
		},
		Layer4: &pb.Layer4{
			Protocol: &pb.Layer4_Tcp{
				Tcp: &pb.TCP{
					SourcePort:      12345,
					DestinationPort: 80,
				},
			},
		},
	}

	s.mockStream.On("Send", mock.MatchedBy(func(req *pb.SendKubernetesNetworkFlowsRequest) bool {
		return req.GetFiveTupleFlow() != nil
	})).Return(nil).Once()

	ctx, cancel := context.WithCancel(context.Background())

	// Send flow and then cancel context
	go func() {
		s.outFlows <- flow

		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := s.client.Run(ctx)

	s.Require().ErrorIs(err, context.Canceled)
	s.mockStream.AssertExpectations(s.T())
}

func (s *NetworkFlowsClientTestSuite) TestRun_SendCiliumFlow() {
	flow := &pb.CiliumFlow{
		NodeName: "test-node",
		Layer3: &pb.IP{
			Source:      "10.0.0.1",
			Destination: "10.0.0.2",
		},
	}

	s.mockStream.On("Send", mock.MatchedBy(func(req *pb.SendKubernetesNetworkFlowsRequest) bool {
		return req.GetCiliumFlow() != nil
	})).Return(nil).Once()

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		s.outFlows <- flow

		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := s.client.Run(ctx)

	s.Require().ErrorIs(err, context.Canceled)
	s.mockStream.AssertExpectations(s.T())
}

func (s *NetworkFlowsClientTestSuite) TestRun_SendError() {
	flow := &pb.FiveTupleFlow{
		Layer3: &pb.IP{
			Source: "10.0.0.1",
		},
	}

	expectedErr := errors.New("send error")
	s.mockStream.On("Send", mock.Anything).Return(expectedErr).Once()

	go func() {
		s.outFlows <- flow
	}()

	err := s.client.Run(context.Background())

	s.Require().ErrorIs(err, expectedErr)
	s.mockStream.AssertExpectations(s.T())
}

func (s *NetworkFlowsClientTestSuite) TestSendKeepalive_Success() {
	s.mockStream.On("Send", mock.MatchedBy(func(req *pb.SendKubernetesNetworkFlowsRequest) bool {
		return req.GetKeepalive() != nil
	})).Return(nil).Once()

	err := s.client.SendKeepalive(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())
}

func (s *NetworkFlowsClientTestSuite) TestSendKeepalive_Error() {
	expectedErr := errors.New("send error")
	s.mockStream.On("Send", mock.Anything).Return(expectedErr).Once()

	err := s.client.SendKeepalive(context.Background())

	s.Require().ErrorIs(err, expectedErr)
	s.mockStream.AssertExpectations(s.T())
}

func (s *NetworkFlowsClientTestSuite) TestSendKeepalive_AfterClose() {
	err := s.client.Close()
	s.Require().NoError(err)

	err = s.client.SendKeepalive(context.Background())

	s.Require().Error(err)
	s.Contains(err.Error(), "stream closed")
}

func (s *NetworkFlowsClientTestSuite) TestClose() {
	err := s.client.Close()

	s.Require().NoError(err)
	s.True(s.client.closed)
}

func (s *NetworkFlowsClientTestSuite) TestClose_Idempotent() {
	err := s.client.Close()
	s.Require().NoError(err)

	err = s.client.Close()
	s.Require().NoError(err)

	s.True(s.client.closed)
}
