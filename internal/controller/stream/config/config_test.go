// Copyright 2026 Illumio, Inc. All Rights Reserved.

package config

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc/metadata"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/logging"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// Mock for the GetConfigurationUpdatesClient.
type MockConfigUpdateClient struct {
	mock.Mock
}

func (m *MockConfigUpdateClient) CloseSend() error {
	args := m.Called()

	return args.Error(0)
}

func (m *MockConfigUpdateClient) Recv() (*pb.GetConfigurationUpdatesResponse, error) {
	args := m.Called()
	if resp, ok := args.Get(0).(*pb.GetConfigurationUpdatesResponse); ok {
		return resp, args.Error(1)
	}

	return nil, args.Error(1)
}

func (m *MockConfigUpdateClient) Send(req *pb.GetConfigurationUpdatesRequest) error {
	args := m.Called(req)

	return args.Error(0)
}

func (m *MockConfigUpdateClient) Context() context.Context {
	return context.Background()
}

func (m *MockConfigUpdateClient) Header() (metadata.MD, error) {
	args := m.Called()
	if header, ok := args.Get(0).(metadata.MD); ok {
		return header, args.Error(1)
	}

	return nil, args.Error(1)
}

func (m *MockConfigUpdateClient) Trailer() metadata.MD {
	args := m.Called()
	if trailer, ok := args.Get(0).(metadata.MD); ok {
		return trailer
	}

	return nil
}

func (m *MockConfigUpdateClient) RecvMsg(msg interface{}) error {
	args := m.Called(msg)

	return args.Error(0)
}

func (m *MockConfigUpdateClient) SendMsg(msg interface{}) error {
	args := m.Called(msg)

	return args.Error(0)
}

// Test suite for Stream.
type ConfigStreamTestSuite struct {
	suite.Suite

	mockClient *MockConfigUpdateClient
	grpcSyncer *logging.BufferedGrpcWriteSyncer
	mockLogger *zap.Logger
}

func (suite *ConfigStreamTestSuite) SetupTest() {
	suite.mockClient = new(MockConfigUpdateClient)
	suite.mockLogger = zap.NewNop()
	suite.grpcSyncer = logging.NewBufferedGrpcWriteSyncerForTest(suite.mockLogger)
}

// Test that log-level updates are applied correctly.
func (suite *ConfigStreamTestSuite) TestLogLevelUpdate() {
	update := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_DEBUG,
			},
		},
	}

	suite.mockClient.On("Recv").Return(update, nil).Once()
	suite.mockClient.On("Recv").Return(nil, io.EOF).Once()

	sm := &stream.Manager{
		BufferedGrpcSyncer: suite.grpcSyncer,
		Client: &stream.Client{
			ConfigurationStream: suite.mockClient,
		},
		PolicyState: stream.NewPolicyState(),
	}

	err := Stream(context.TODO(), sm, suite.mockLogger, 1*time.Second)
	suite.Require().NoError(err)

	suite.Equal(zapcore.DebugLevel, suite.grpcSyncer.GetLogLevel())

	suite.mockClient.AssertExpectations(suite.T())
}

// Test handling of stream closure.
func (suite *ConfigStreamTestSuite) TestStreamEOF() {
	suite.mockClient.On("Recv").Return(nil, io.EOF).Once()

	sm := &stream.Manager{
		BufferedGrpcSyncer: suite.grpcSyncer,
		Client: &stream.Client{
			ConfigurationStream: suite.mockClient,
		},
		PolicyState: stream.NewPolicyState(),
	}

	err := Stream(context.TODO(), sm, suite.mockLogger, 1*time.Second)
	suite.Require().NoError(err)

	suite.mockClient.AssertExpectations(suite.T())
}

// Test handling of stream errors.
func (suite *ConfigStreamTestSuite) TestStreamError() {
	suite.mockClient.On("Recv").Return(nil, io.ErrUnexpectedEOF).Once()

	sm := &stream.Manager{
		BufferedGrpcSyncer: suite.grpcSyncer,
		Client: &stream.Client{
			ConfigurationStream: suite.mockClient,
		},
		PolicyState: stream.NewPolicyState(),
	}

	err := Stream(context.TODO(), sm, suite.mockLogger, 1*time.Second)

	suite.Require().Error(err, "Expected Stream to return an error on unexpected EOF")

	suite.mockClient.AssertExpectations(suite.T())
}

// Test handling of unknown configuration updates.
func (suite *ConfigStreamTestSuite) TestUnknownConfigurationUpdate() {
	unknownUpdate := &pb.GetConfigurationUpdatesResponse{
		Response: nil,
	}

	suite.mockClient.On("Recv").Return(unknownUpdate, nil).Once()
	suite.mockClient.On("Recv").Return(nil, io.EOF).Once()

	sm := &stream.Manager{
		BufferedGrpcSyncer: suite.grpcSyncer,
		Client: &stream.Client{
			ConfigurationStream: suite.mockClient,
		},
		PolicyState: stream.NewPolicyState(),
	}

	err := Stream(context.TODO(), sm, suite.mockLogger, 1*time.Second)
	suite.Require().NoError(err)

	suite.mockClient.AssertExpectations(suite.T())
}

// Run the test suite.
func TestConfigStreamTestSuite(t *testing.T) {
	suite.Run(t, new(ConfigStreamTestSuite))
}
