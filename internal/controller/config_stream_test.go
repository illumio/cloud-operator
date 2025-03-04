// Copyright 2025 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"io"
	"testing"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"
)

// Mock for the SendConfigurationUpdatesClient
type MockConfigUpdateClient struct {
	mock.Mock
}

// Implement missing CloseSend method
func (m *MockConfigUpdateClient) CloseSend() error {
	args := m.Called()
	return args.Error(0)
}

// Implement missing Recv method
func (m *MockConfigUpdateClient) Recv() (*pb.GetConfigurationUpdatesResponse, error) {
	args := m.Called()
	if resp, ok := args.Get(0).(*pb.GetConfigurationUpdatesResponse); ok {
		return resp, args.Error(1)
	}
	return nil, args.Error(1)
}

// Implement missing Send method
func (m *MockConfigUpdateClient) Send(req *pb.GetConfigurationUpdatesRequest) error {
	args := m.Called(req)
	return args.Error(0)
}

// Implement missing Context method
func (m *MockConfigUpdateClient) Context() context.Context {
	return context.Background()
}

// Implement missing Header method
func (m *MockConfigUpdateClient) Header() (metadata.MD, error) {
	args := m.Called()
	if header, ok := args.Get(0).(metadata.MD); ok {
		return header, args.Error(1)
	}
	return nil, args.Error(1)
}

// Implement missing Trailer method
func (m *MockConfigUpdateClient) Trailer() metadata.MD {
	args := m.Called()
	if trailer, ok := args.Get(0).(metadata.MD); ok {
		return trailer
	}
	return nil
}

// Implement missing RecvMsg method
func (m *MockConfigUpdateClient) RecvMsg(msg interface{}) error {
	args := m.Called(msg)
	return args.Error(0)
}

// Implement missing SendMsg method
func (m *MockConfigUpdateClient) SendMsg(msg interface{}) error {
	args := m.Called(msg)
	return args.Error(0)
}

// Test suite for ListenToConfigurationStream
type ConfigStreamTestSuite struct {
	suite.Suite
	mockClient *MockConfigUpdateClient
	grpcSyncer *BufferedGrpcWriteSyncer
	mockLogger *zap.Logger
}

func (suite *ConfigStreamTestSuite) SetupTest() {
	suite.mockClient = new(MockConfigUpdateClient)
	suite.mockLogger = zap.NewNop() // Use a no-op zap.Logger instead of SugaredLogger
	suite.grpcSyncer = &BufferedGrpcWriteSyncer{
		logger:   suite.mockLogger,
		logLevel: zap.NewAtomicLevel(),
	}
}

// Test that log-level updates are applied correctly
func (suite *ConfigStreamTestSuite) TestLogLevelUpdate() {
	// Simulate receiving a log-level change
	update := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_SetLogLevel{
			SetLogLevel: &pb.SetLogLevel{
				Level: pb.LogLevel_LOG_LEVEL_DEBUG,
			},
		},
	}

	// Mock the `Recv` method call once and return the update response
	suite.mockClient.On("Recv").Return(update, nil).Once()
	suite.mockClient.On("Recv").Return(nil, io.EOF).Once()

	err := ListenToConfigurationStream(suite.mockClient, suite.grpcSyncer)
	suite.NoError(err)

	// Verify that log level was updated
	suite.Equal(zap.DebugLevel, suite.grpcSyncer.logLevel.Level())

	// Ensure all expectations were met
	suite.mockClient.AssertExpectations(suite.T())
}

// Test handling of stream closure
func (suite *ConfigStreamTestSuite) TestStreamEOF() {
	// Mock EOF response
	suite.mockClient.On("Recv").Return(nil, io.EOF).Once()

	err := ListenToConfigurationStream(suite.mockClient, suite.grpcSyncer)
	suite.NoError(err)

	// Ensure all expectations were met
	suite.mockClient.AssertExpectations(suite.T())
}

// Test handling of stream errors
func (suite *ConfigStreamTestSuite) TestStreamError() {
	// Mock an unexpected EOF error
	suite.mockClient.On("Recv").Return(nil, io.ErrUnexpectedEOF).Once()

	err := ListenToConfigurationStream(suite.mockClient, suite.grpcSyncer)
	suite.Error(err)

	// Ensure all expectations were met
	suite.mockClient.AssertExpectations(suite.T())
}

// Run the test suite
func TestConfigStreamTestSuite(t *testing.T) {
	suite.Run(t, new(ConfigStreamTestSuite))
}
