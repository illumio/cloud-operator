package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/metadata"
)

// MockSendLogsClient mocks the SendLogsClient gRPC interface
type MockSendLogsClient struct {
	mock.Mock
}

func (m *MockSendLogsClient) Send(req *pb.SendLogsRequest) error {
	args := m.Called(req)
	return args.Error(0)
}

func (m *MockSendLogsClient) Recv() (*pb.SendLogsResponse, error) {
	args := m.Called()
	if resp, ok := args.Get(0).(*pb.SendLogsResponse); ok {
		return resp, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockSendLogsClient) CloseSend() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockSendLogsClient) Context() context.Context {
	return context.Background()
}

func (m *MockSendLogsClient) Header() (metadata.MD, error) {
	args := m.Called()
	if header, ok := args.Get(0).(metadata.MD); ok {
		return header, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockSendLogsClient) Trailer() metadata.MD {
	args := m.Called()
	if trailer, ok := args.Get(0).(metadata.MD); ok {
		return trailer
	}
	return nil
}

func (m *MockSendLogsClient) SendMsg(msg interface{}) error {
	args := m.Called(msg)
	return args.Error(0)
}

func (m *MockSendLogsClient) RecvMsg(msg interface{}) error {
	args := m.Called(msg)
	return args.Error(0)
}

// MockClientConn mocks ClientConnInterface
type MockClientConn struct {
	mock.Mock
}

func (m *MockClientConn) GetState() connectivity.State {
	args := m.Called()
	return args.Get(0).(connectivity.State)
}

func (m *MockClientConn) Close() error {
	args := m.Called()
	return args.Error(0)
}

// BufferedGrpcWriteSyncerTestSuite is a test suite for BufferedGrpcWriteSyncer
type BufferedGrpcWriteSyncerTestSuite struct {
	suite.Suite
	grpcSyncer *BufferedGrpcWriteSyncer
	mockClient *MockSendLogsClient
	mockConn   *MockClientConn
}

// TestBufferedGrpcWriteSyncerTestSuite runs the test suite
func TestBufferedGrpcWriteSyncerTestSuite(t *testing.T) {
	suite.Run(t, new(BufferedGrpcWriteSyncerTestSuite))
}

func (suite *BufferedGrpcWriteSyncerTestSuite) SetupTest() {
	mockClient := &MockSendLogsClient{}
	mockConn := &MockClientConn{}
	encoderConfig := zap.NewProductionEncoderConfig()
	encoder := zapcore.NewJSONEncoder(encoderConfig)

	suite.mockClient = mockClient
	suite.mockConn = mockConn
	suite.grpcSyncer = &BufferedGrpcWriteSyncer{
		client:   mockClient,
		conn:     mockConn,
		buffer:   make([]string, 0, maxBufferSize),
		done:     make(chan struct{}),
		logger:   zap.NewNop(), // Use a no-op logger for simplicity
		logLevel: zap.NewAtomicLevel(),
		encoder:  encoder,
	}

	mockConn.On("GetState").Return(connectivity.Ready)
	mockConn.On("Close").Return(nil)
}

// TestSendLogEntry tests the sendLogEntry method to ensure proper formatting and encoding
func (suite *BufferedGrpcWriteSyncerTestSuite) TestSendLogEntry() {
	ts, err := time.Parse(time.RFC3339, "2025-02-28T11:56:05Z")
	suite.NoError(err)

	entry := zapcore.Entry{
		Level: zapcore.InfoLevel,
		Time:  ts,
		// Message contains the entry's whole structured context already serialized.
		// gRPC logger requires that this is serialized into a JSON object.
		Message: "The Message",
	}

	fields := []zap.Field{
		zap.String("field1", "a string"),
		zap.Int("field2", 10),
	}

	jsonMessage, err := encodeLogEntry(suite.grpcSyncer.encoder, entry, fields)
	suite.NoError(err)

	expectedLogEntry := &pb.LogEntry{
		JsonMessage: `{"level":"info","ts":1740743765,"msg":"The Message","field1":"a string","field2":10}`,
	}

	suite.mockClient.On("Send", &pb.SendLogsRequest{
		Request: &pb.SendLogsRequest_LogEntry{
			LogEntry: expectedLogEntry,
		},
	}).Return(nil).Once()

	err = suite.grpcSyncer.sendLogEntry(jsonMessage)
	suite.NoError(err)
	suite.mockClient.AssertExpectations(suite.T())
}

// mockZapClock mocks zapcore.Clock to always return the same time for "now".
type mockZapClock struct {
	now time.Time
}

var _ zapcore.Clock = &mockZapClock{}

func (c *mockZapClock) Now() time.Time {
	return c.now
}

func (c *mockZapClock) NewTicker(duration time.Duration) *time.Ticker {
	return time.NewTicker(duration)
}

// TestZapCoreWrapper tests the gRPC logger end-to-end.
func (suite *BufferedGrpcWriteSyncerTestSuite) TestZapCoreWrapper() {
	ts, err := time.Parse(time.RFC3339, "2025-02-28T11:56:05Z")
	suite.NoError(err)

	mockClock := &mockZapClock{
		now: ts,
	}

	// Disable logging the caller and mock the clock to make the test deterministic
	logger := NewGRPCLogger(suite.grpcSyncer, false, mockClock)

	expectedLogEntry := &pb.LogEntry{
		JsonMessage: `{"level":"info","ts":"2025-02-28T11:56:05Z","msg":"The Message","field1":"a string","field2":10,"error":"some error"}`,
	}

	suite.mockClient.On("Send", &pb.SendLogsRequest{
		Request: &pb.SendLogsRequest_LogEntry{
			LogEntry: expectedLogEntry,
		},
	}).Return(nil).Once()

	logger = logger.With(
		zap.String("field1", "a string"),
	)

	logger.Info("The Message",
		zap.Int("field2", 10),
		zap.Error(errors.New("some error")),
	)

	suite.mockClient.AssertExpectations(suite.T())
}
