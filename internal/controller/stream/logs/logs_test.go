// Copyright 2026 Illumio, Inc. All Rights Reserved.

package logs

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/logging"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// Mock for the SendLogsClient.
type MockLogStreamClient struct {
	mock.Mock
}

func (m *MockLogStreamClient) CloseSend() error {
	args := m.Called()

	return args.Error(0)
}

func (m *MockLogStreamClient) Recv() (*pb.SendLogsResponse, error) {
	args := m.Called()
	if resp, ok := args.Get(0).(*pb.SendLogsResponse); ok {
		return resp, args.Error(1)
	}

	return nil, args.Error(1)
}

func (m *MockLogStreamClient) Send(req *pb.SendLogsRequest) error {
	args := m.Called(req)

	return args.Error(0)
}

func (m *MockLogStreamClient) Context() context.Context {
	return context.Background()
}

func (m *MockLogStreamClient) Header() (metadata.MD, error) {
	args := m.Called()
	if header, ok := args.Get(0).(metadata.MD); ok {
		return header, args.Error(1)
	}

	return nil, args.Error(1)
}

func (m *MockLogStreamClient) Trailer() metadata.MD {
	args := m.Called()
	if trailer, ok := args.Get(0).(metadata.MD); ok {
		return trailer
	}

	return nil
}

func (m *MockLogStreamClient) RecvMsg(msg any) error {
	args := m.Called(msg)

	return args.Error(0)
}

func (m *MockLogStreamClient) SendMsg(msg any) error {
	args := m.Called(msg)

	return args.Error(0)
}

// Test suite for Log Stream.
type LogStreamTestSuite struct {
	suite.Suite

	mockClient *MockLogStreamClient
	grpcSyncer *logging.BufferedGrpcWriteSyncer
	mockLogger *zap.Logger
}

func (suite *LogStreamTestSuite) SetupTest() {
	suite.mockClient = new(MockLogStreamClient)
	suite.mockLogger = zap.NewNop()
	suite.grpcSyncer = logging.NewBufferedGrpcWriteSyncerForTest(suite.mockLogger)
}

// Test handling of stream closure (EOF).
func (suite *LogStreamTestSuite) TestStreamEOF() {
	suite.mockClient.On("Recv").Return(nil, io.EOF).Once()

	sm := &stream.Manager{
		BufferedGrpcSyncer: suite.grpcSyncer,
		Client: &stream.Client{
			LogStream: suite.mockClient,
		},
	}

	err := Stream(context.TODO(), sm, suite.mockLogger, 1*time.Second)
	suite.Require().NoError(err)

	suite.mockClient.AssertExpectations(suite.T())
}

// Test handling of stream errors.
func (suite *LogStreamTestSuite) TestStreamError() {
	suite.mockClient.On("Recv").Return(nil, io.ErrUnexpectedEOF).Once()

	sm := &stream.Manager{
		BufferedGrpcSyncer: suite.grpcSyncer,
		Client: &stream.Client{
			LogStream: suite.mockClient,
		},
	}

	err := Stream(context.TODO(), sm, suite.mockLogger, 1*time.Second)

	suite.Require().Error(err, "Expected Stream to return an error on unexpected EOF")

	suite.mockClient.AssertExpectations(suite.T())
}

// Run the test suite.
func TestLogStreamTestSuite(t *testing.T) {
	suite.Run(t, new(LogStreamTestSuite))
}
