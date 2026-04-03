// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// mockKubernetesInfoServiceClient mocks the pb.KubernetesInfoServiceClient interface.
type mockKubernetesInfoServiceClient struct {
	mock.Mock
}

func (m *mockKubernetesInfoServiceClient) GetConfigurationUpdates(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[pb.GetConfigurationUpdatesRequest, pb.GetConfigurationUpdatesResponse], error) {
	args := m.Called(ctx, opts)
	if stream, ok := args.Get(0).(grpc.BidiStreamingClient[pb.GetConfigurationUpdatesRequest, pb.GetConfigurationUpdatesResponse]); ok {
		return stream, args.Error(1)
	}

	return nil, args.Error(1)
}

func (m *mockKubernetesInfoServiceClient) SendKubernetesResources(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[pb.SendKubernetesResourcesRequest, pb.SendKubernetesResourcesResponse], error) {
	args := m.Called(ctx, opts)
	if stream, ok := args.Get(0).(grpc.BidiStreamingClient[pb.SendKubernetesResourcesRequest, pb.SendKubernetesResourcesResponse]); ok {
		return stream, args.Error(1)
	}

	return nil, args.Error(1)
}

func (m *mockKubernetesInfoServiceClient) SendKubernetesNetworkFlows(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[pb.SendKubernetesNetworkFlowsRequest, pb.SendKubernetesNetworkFlowsResponse], error) {
	args := m.Called(ctx, opts)
	if stream, ok := args.Get(0).(grpc.BidiStreamingClient[pb.SendKubernetesNetworkFlowsRequest, pb.SendKubernetesNetworkFlowsResponse]); ok {
		return stream, args.Error(1)
	}

	return nil, args.Error(1)
}

func (m *mockKubernetesInfoServiceClient) SendLogs(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[pb.SendLogsRequest, pb.SendLogsResponse], error) {
	args := m.Called(ctx, opts)
	if stream, ok := args.Get(0).(grpc.BidiStreamingClient[pb.SendLogsRequest, pb.SendLogsResponse]); ok {
		return stream, args.Error(1)
	}

	return nil, args.Error(1)
}

// mockNetworkFlowsBidiStream mocks the BidiStreamingClient for network flows.
type mockNetworkFlowsBidiStream struct {
	mock.Mock
	grpc.ClientStream
}

func (m *mockNetworkFlowsBidiStream) Send(req *pb.SendKubernetesNetworkFlowsRequest) error {
	args := m.Called(req)

	return args.Error(0)
}

func (m *mockNetworkFlowsBidiStream) Recv() (*pb.SendKubernetesNetworkFlowsResponse, error) {
	args := m.Called()
	if resp, ok := args.Get(0).(*pb.SendKubernetesNetworkFlowsResponse); ok {
		return resp, args.Error(1)
	}

	return nil, args.Error(1)
}

func (m *mockNetworkFlowsBidiStream) CloseAndRecv() (*pb.SendKubernetesNetworkFlowsResponse, error) {
	args := m.Called()
	if resp, ok := args.Get(0).(*pb.SendKubernetesNetworkFlowsResponse); ok {
		return resp, args.Error(1)
	}

	return nil, args.Error(1)
}

func (m *mockNetworkFlowsBidiStream) CloseSend() error {
	args := m.Called()

	return args.Error(0)
}

func TestNetworkFlowsFactory_Name(t *testing.T) {
	factory := &NetworkFlowsFactory{}

	name := factory.Name()

	assert.Equal(t, "SendKubernetesNetworkFlows", name)
}

func TestNetworkFlowsFactory_ImplementsInterface(t *testing.T) {
	factory := &NetworkFlowsFactory{}

	var _ stream.StreamClientFactory = factory
}

func TestNetworkFlowsFactory_NewStreamClient_Success(t *testing.T) {
	logger := zap.NewNop()
	stats := stream.NewStats()
	outFlows := make(chan pb.Flow, 10)
	flowCache := stream.NewFlowCache(10*time.Second, 100, outFlows)

	defer func() {
		_ = flowCache.Close()
	}()

	factory := &NetworkFlowsFactory{
		Logger:    logger,
		FlowCache: flowCache,
		Stats:     stats,
	}

	mockClient := &mockKubernetesInfoServiceClient{}
	mockStream := &mockNetworkFlowsBidiStream{}

	mockClient.On("SendKubernetesNetworkFlows", mock.Anything, mock.Anything).Return(mockStream, nil).Once()

	client, err := factory.NewStreamClient(context.Background(), mockClient)

	require.NoError(t, err)
	require.NotNil(t, client)
	mockClient.AssertExpectations(t)
}

func TestNetworkFlowsFactory_NewStreamClient_Error(t *testing.T) {
	logger := zap.NewNop()
	stats := stream.NewStats()
	outFlows := make(chan pb.Flow, 10)
	flowCache := stream.NewFlowCache(10*time.Second, 100, outFlows)

	defer func() {
		_ = flowCache.Close()
	}()

	factory := &NetworkFlowsFactory{
		Logger:    logger,
		FlowCache: flowCache,
		Stats:     stats,
	}

	mockClient := &mockKubernetesInfoServiceClient{}
	expectedErr := errors.New("connection error")

	mockClient.On("SendKubernetesNetworkFlows", mock.Anything, mock.Anything).Return(nil, expectedErr).Once()

	client, err := factory.NewStreamClient(context.Background(), mockClient)

	require.ErrorIs(t, err, expectedErr)
	require.Nil(t, client)
	mockClient.AssertExpectations(t)
}
