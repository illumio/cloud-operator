// Copyright 2026 Illumio, Inc. All Rights Reserved.

package logs

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/logging"
)

// mockLogStream mocks the logging.LogStream interface.
type mockLogStream struct {
	mock.Mock
}

func (m *mockLogStream) Send(req *pb.SendLogsRequest) error {
	args := m.Called(req)

	return args.Error(0)
}

func (m *mockLogStream) Recv() (*pb.SendLogsResponse, error) {
	args := m.Called()
	if resp, ok := args.Get(0).(*pb.SendLogsResponse); ok {
		return resp, args.Error(1)
	}

	return nil, args.Error(1)
}

func (m *mockLogStream) CloseSend() error {
	args := m.Called()

	return args.Error(0)
}

// mockClientConn mocks the grpc.ClientConnInterface for testing.
type mockClientConn struct {
	mock.Mock
}

func (m *mockClientConn) GetState() connectivity.State {
	args := m.Called()

	return args.Get(0).(connectivity.State) //nolint:forcetypeassert
}

func (m *mockClientConn) Close() error {
	args := m.Called()

	return args.Error(0)
}

// LogsClientTestSuite tests the logsClient.
type LogsClientTestSuite struct {
	suite.Suite

	mockStream *mockLogStream
	mockConn   *mockClientConn
	syncer     *logging.BufferedGrpcWriteSyncer
	logger     *zap.Logger
	client     *logsClient
}

func TestLogsClientTestSuite(t *testing.T) {
	suite.Run(t, new(LogsClientTestSuite))
}

func (s *LogsClientTestSuite) SetupTest() {
	s.mockStream = &mockLogStream{}
	s.mockConn = &mockClientConn{}
	s.logger = zap.NewNop()
	s.syncer = logging.NewBufferedGrpcWriteSyncerForTest(s.logger)

	// Setup default mock expectations for conn
	s.mockConn.On("GetState").Return(connectivity.Ready).Maybe()
	s.mockConn.On("Close").Return(nil).Maybe()

	s.client = &logsClient{
		stream:             s.mockStream,
		conn:               (*grpc.ClientConn)(nil), // Will use mockConn via syncer
		logger:             s.logger,
		bufferedGrpcSyncer: s.syncer,
		done:               make(chan struct{}),
	}
}

func (s *LogsClientTestSuite) TestRun_ContextCanceled() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := s.client.Run(ctx)

	s.Require().ErrorIs(err, context.Canceled)
}

func (s *LogsClientTestSuite) TestRun_EOF() {
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())
}

func (s *LogsClientTestSuite) TestRun_StreamError() {
	expectedErr := errors.New("stream error")
	s.mockStream.On("Recv").Return(nil, expectedErr).Once()

	err := s.client.Run(context.Background())

	s.Require().ErrorIs(err, expectedErr)
	s.mockStream.AssertExpectations(s.T())
}

func (s *LogsClientTestSuite) TestSendKeepalive_NoOp() {
	// SendKeepalive for logs is a no-op (handled by BufferedGrpcWriteSyncer)
	err := s.client.SendKeepalive(context.Background())

	s.Require().NoError(err)
}

func (s *LogsClientTestSuite) TestClose() {
	err := s.client.Close()

	s.Require().NoError(err)
}
