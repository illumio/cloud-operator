package controller

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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
		buffer:   make([]string, 0, logMaxBufferSize),
		done:     make(chan struct{}),
		logger:   zap.NewNop(), // Use a no-op logger for simplicity
		logLevel: zap.NewAtomicLevel(),
		encoder:  encoder,
	}

	mockConn.On("GetState").Return(connectivity.Ready)
	mockConn.On("Close").Return(nil)
}

// TestBufferFlushOnReconnect ensures buffered logs are sent after reconnection.
func (suite *BufferedGrpcWriteSyncerTestSuite) TestBufferFlushOnReconnect() {
	expectedMessage := `{"level":"info","ts":1740743765,"msg":"Buffered log entry"}`
	suite.grpcSyncer.buffer = append(suite.grpcSyncer.buffer, expectedMessage)

	suite.mockClient.On("Send", &pb.SendLogsRequest{
		Request: &pb.SendLogsRequest_LogEntry{
			LogEntry: &pb.LogEntry{
				JsonMessage: expectedMessage,
			},
		},
	}).Return(nil).Once()

	suite.grpcSyncer.flush()

	suite.mockClient.AssertExpectations(suite.T())
	suite.Empty(suite.grpcSyncer.buffer, "Buffer should be empty after flushing")
}

// TestUpdateLogLevel verifies that log levels are correctly updated.
func (suite *BufferedGrpcWriteSyncerTestSuite) TestUpdateLogLevel() {
	logLevels := map[pb.LogLevel]zapcore.Level{
		pb.LogLevel_LOG_LEVEL_DEBUG: zapcore.DebugLevel,
		pb.LogLevel_LOG_LEVEL_INFO:  zapcore.InfoLevel,
		pb.LogLevel_LOG_LEVEL_WARN:  zapcore.WarnLevel,
		pb.LogLevel_LOG_LEVEL_ERROR: zapcore.ErrorLevel,
	}

	for grpcLevel, expectedZapLevel := range logLevels {
		suite.Run(fmt.Sprintf("Set log level %v", grpcLevel), func() {
			suite.grpcSyncer.updateLogLevel(grpcLevel)
			suite.Equal(expectedZapLevel, suite.grpcSyncer.logLevel.Level())
		})
	}
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

// TestWriteBuffering tests the gRPC logger's buffering when the connection is not established.
func (suite *BufferedGrpcWriteSyncerTestSuite) TestWriteBuffering() {
	ts, err := time.Parse(time.RFC3339, "2025-02-28T11:56:05Z")
	suite.NoError(err)

	mockClock := &mockZapClock{
		now: ts,
	}

	// Disable logging the caller and mock the clock to make the test deterministic
	logger := NewGRPCLogger(suite.grpcSyncer, false, mockClock)

	// Equivalent to a disconnection
	suite.grpcSyncer.conn = nil

	expectedLostLogEntriesCount := 0

	for i := 0; i < logMaxBufferSize+10; i += 1 {
		suite.Run(fmt.Sprintf("Message %d", i), func() {
			logger.Info("The Message",
				zap.Int("num", i),
			)

			if i < logMaxBufferSize {
				expectedJsonMessage := fmt.Sprintf(`{"level":"info","ts":"2025-02-28T11:56:05Z","msg":"The Message","num":%d}`, i)
				suite.Equal(i+1, len(suite.grpcSyncer.buffer))
				suite.Equal(expectedJsonMessage, suite.grpcSyncer.buffer[i])
			} else {
				suite.Equal(logMaxBufferSize, len(suite.grpcSyncer.buffer))
				expectedLostLogEntriesCount += 1
				suite.Equal(expectedLostLogEntriesCount, suite.grpcSyncer.lostLogEntriesCount)
			}
		})
	}
}

func (suite *BufferedGrpcWriteSyncerTestSuite) TestFlushSuccess() {
	// Simulate buffer overflow
	suite.grpcSyncer.lostLogEntriesCount = 1234
	suite.grpcSyncer.lostLogEntriesErr = errors.New("some buffer overflow error")
	suite.grpcSyncer.buffer = append(
		suite.grpcSyncer.buffer,
		`{"level":"info","msg":"Buffered log entry 1"}`,
		`{"level":"info","msg":"Buffered log entry 2"}`,
		`{"level":"info","msg":"Buffered log entry 3"}`,
		`{"level":"info","msg":"Buffered log entry 4"}`,
		`{"level":"info","msg":"Buffered log entry 5"}`,
	)

	suite.mockClient.On("Send", mock.MatchedBy(func(req *pb.SendLogsRequest) bool {
		switch {
		// Send the 'Lost logs' message
		case regexp.MustCompile(
			`^{"level":"error","ts":[^,]*,"msg":"Lost logs due to buffer overflow","error":"some buffer overflow error","lost_log_entries":1234}$`,
		).MatchString(req.Request.(*pb.SendLogsRequest_LogEntry).LogEntry.JsonMessage):
			return true

		// Send all buffered messages
		case regexp.MustCompile(
			`^{"level":"info","msg":"Buffered log entry [12345]"}$`,
		).MatchString(req.Request.(*pb.SendLogsRequest_LogEntry).LogEntry.JsonMessage):
			return true

		default:
			return false
		}
	})).Return(nil).Times(6)

	// Flush the buffer
	suite.grpcSyncer.flush()

	// Assert that lostLogEntriesCount is reset
	suite.Equal(0, suite.grpcSyncer.lostLogEntriesCount, "lostLogEntriesCount should be reset after successfully sending 'Lost logs' message")
	suite.Empty(suite.grpcSyncer.buffer, "Buffer should be empty after succesful flush")
	suite.Nil(suite.grpcSyncer.lostLogEntriesErr, "lostLogEntriesErr should be nil after successfully sending 'Lost logs' message and no subsequent failure")
}

func (suite *BufferedGrpcWriteSyncerTestSuite) TestFlushPartialSuccess() {
	// Simulate buffer overflow
	suite.grpcSyncer.lostLogEntriesCount = 1234
	suite.grpcSyncer.lostLogEntriesErr = errors.New("some buffer overflow error")
	suite.grpcSyncer.buffer = append(
		suite.grpcSyncer.buffer,
		`{"level":"info","msg":"Buffered log entry 1"}`,
		`{"level":"info","msg":"Buffered log entry 2"}`,
		`{"level":"info","msg":"Buffered log entry 3"}`,
		`{"level":"info","msg":"Buffered log entry 4"}`,
		`{"level":"info","msg":"Buffered log entry 5"}`,
	)

	suite.mockClient.On("Send", mock.MatchedBy(func(req *pb.SendLogsRequest) bool {
		switch {
		// Send the 'Lost logs' message
		case regexp.MustCompile(
			`^{"level":"error","ts":[^,]*,"msg":"Lost logs due to buffer overflow","error":"some buffer overflow error","lost_log_entries":1234}$`,
		).MatchString(req.Request.(*pb.SendLogsRequest_LogEntry).LogEntry.JsonMessage):
			return true

		// Send buffered messages 1 and 2
		case regexp.MustCompile(
			`^{"level":"info","msg":"Buffered log entry [12]"}$`,
		).MatchString(req.Request.(*pb.SendLogsRequest_LogEntry).LogEntry.JsonMessage):
			return true

		default:
			return false
		}
	})).Return(nil).Times(3)

	suite.mockClient.On("Send", mock.MatchedBy(func(req *pb.SendLogsRequest) bool {
		switch {
		// Fail buffered messages 3, 4, and 5
		case regexp.MustCompile(
			`^{"level":"info","msg":"Buffered log entry [345]"}$`,
		).MatchString(req.Request.(*pb.SendLogsRequest_LogEntry).LogEntry.JsonMessage):
			return true

		default:
			return false
		}
	})).Return(errors.New("send error")).Once()

	// Flush the buffer
	suite.grpcSyncer.flush()

	// Assert that lostLogEntriesCount is reset
	suite.Equal(0, suite.grpcSyncer.lostLogEntriesCount, "lostLogEntriesCount should be reset after successfully sending 'Lost logs' message")
	suite.Len(suite.grpcSyncer.buffer, 3, "Buffer should still contain 3 messages after flushing 2 messages")
	suite.ErrorContains(suite.grpcSyncer.lostLogEntriesErr, "send error", "lostLogEntriesErr should contain the last Send error")
}

func (suite *BufferedGrpcWriteSyncerTestSuite) TestFlushSendLostLogsFailed() {
	// Simulate buffer overflow
	suite.grpcSyncer.lostLogEntriesCount = 1234
	suite.grpcSyncer.lostLogEntriesErr = errors.New("some buffer overflow error")
	suite.grpcSyncer.buffer = append(
		suite.grpcSyncer.buffer,
		`{"level":"info","msg":"Buffered log entry 1"}`,
		`{"level":"info","msg":"Buffered log entry 2"}`,
		`{"level":"info","msg":"Buffered log entry 3"}`,
		`{"level":"info","msg":"Buffered log entry 4"}`,
		`{"level":"info","msg":"Buffered log entry 5"}`,
	)

	suite.mockClient.On("Send", mock.MatchedBy(func(req *pb.SendLogsRequest) bool {
		switch {
		// Fail the 'Lost logs' message
		case regexp.MustCompile(
			`^{"level":"error","ts":[^,]*,"msg":"Lost logs due to buffer overflow","error":"some buffer overflow error","lost_log_entries":1234}$`,
		).MatchString(req.Request.(*pb.SendLogsRequest_LogEntry).LogEntry.JsonMessage):
			return true

		default:
			return false
		}
	})).Return(errors.New("send error")).Once()

	// Flush the buffer
	suite.grpcSyncer.flush()

	// Assert that lostLogEntriesCount is reset
	suite.Equal(1234, suite.grpcSyncer.lostLogEntriesCount, "lostLogEntriesCount should not be reset after failing to send 'Lost logs' message")
	suite.Len(suite.grpcSyncer.buffer, 5, "Buffer should still contain 3 messages after flushing 2 messages")
	suite.ErrorContains(suite.grpcSyncer.lostLogEntriesErr, "send error", "lostLogEntriesErr should contain the last Send error")
}

func (suite *BufferedGrpcWriteSyncerTestSuite) TestLostLogEntriesCountIncrementOnBufferOverflow() {
	// Fill the buffer to its capacity
	for i := 0; i < logMaxBufferSize; i++ {
		suite.grpcSyncer.buffer = append(suite.grpcSyncer.buffer, fmt.Sprintf(`{"level":"info","msg":"Buffered log entry %d"}`, i+1))
	}

	// Attempt to write a new log entry, causing overflow
	suite.mockClient.On("Send", mock.Anything).Return(errors.New("send error")).Times(logMaxBufferSize + 1)
	suite.grpcSyncer.write(`{"level":"info","msg":"Overflow log entry"}`)

	// Assert that lostLogEntriesCount is incremented
	suite.Equal(1, suite.grpcSyncer.lostLogEntriesCount, "lostLogEntriesCount should be incremented on buffer overflow")
	suite.Equal(logMaxBufferSize, len(suite.grpcSyncer.buffer), "Buffer should remain at max capacity")
	suite.ErrorContains(suite.grpcSyncer.lostLogEntriesErr, "send error", "lostLogEntriesErr should contain the last Send error")
}

// TestNewBufferedGrpcWriteSyncer tests the constructor
func (suite *BufferedGrpcWriteSyncerTestSuite) TestNewBufferedGrpcWriteSyncer() {
	bws := NewBufferedGrpcWriteSyncer()
	suite.NotNil(bws)
	suite.NotNil(bws.buffer)
	suite.Equal(0, len(bws.buffer))
	suite.Equal(logMaxBufferSize, cap(bws.buffer))
	suite.NotNil(bws.done)
}

// mockSyncer is a mock implementation of zapcore.WriteSyncer
type mockSyncer struct{}

func (m *mockSyncer) Write(p []byte) (n int, err error) {
	return len(p), nil
}

func (m *mockSyncer) Sync() error {
	return nil
}

// TestZapCoreWrapperMethods tests the zapCoreWrapper methods
func (suite *BufferedGrpcWriteSyncerTestSuite) TestZapCoreWrapperMethods() {
	// Create a test core with a mock syncer instead of stdout
	mockSyncer := &mockSyncer{}
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(mockSyncer),
		zapcore.InfoLevel,
	)

	wrapper := &zapCoreWrapper{
		core:       core,
		encoder:    zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		grpcSyncer: suite.grpcSyncer,
	}

	entry := zapcore.Entry{Level: zapcore.InfoLevel}
	checkedEntry := wrapper.Check(entry, nil)
	suite.NotNil(checkedEntry)

	suite.True(wrapper.Enabled(zapcore.InfoLevel))
	suite.False(wrapper.Enabled(zapcore.DebugLevel))

	suite.NoError(wrapper.Sync())

	fields := []zapcore.Field{zap.String("test", "value")}
	newWrapper := wrapper.With(fields)
	suite.NotNil(newWrapper)
	suite.NotEqual(wrapper, newWrapper)
}

// TestClose tests the Close method
func (suite *BufferedGrpcWriteSyncerTestSuite) TestClose() {
	// Set up expectations
	suite.mockConn.On("GetState").Return(connectivity.Ready)
	suite.mockClient.On("Send", mock.Anything).Return(nil)
	suite.mockConn.On("Close").Return(nil)

	suite.grpcSyncer.buffer = append(suite.grpcSyncer.buffer, "test message")

	err := suite.grpcSyncer.Close()
	suite.NoError(err)

	select {
	case <-suite.grpcSyncer.done:

	default:
		suite.Fail("Channel should be closed")
	}

	suite.mockConn.AssertExpectations(suite.T())
	suite.mockClient.AssertExpectations(suite.T())
}

// TestRun tests the run method
func (suite *BufferedGrpcWriteSyncerTestSuite) TestRun() {
	// Create a new syncer for this test with proper initialization
	bws := &BufferedGrpcWriteSyncer{
		client:   suite.mockClient,
		conn:     suite.mockConn,
		buffer:   make([]string, 0, logMaxBufferSize),
		done:     make(chan struct{}),
		logger:   zap.NewNop(),
		logLevel: zap.NewAtomicLevel(),
		encoder:  zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
	}

	bws.buffer = append(bws.buffer, "test message")

	suite.mockConn.On("GetState").Return(connectivity.Ready)
	suite.mockClient.On("Send", mock.Anything).Return(nil)

	// Close the syncer after a short delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		bws.Close()
	}()

	go bws.run()

	time.Sleep(200 * time.Millisecond)

	suite.Equal(0, len(bws.buffer))

	// Verify all expectations were met
	suite.mockConn.AssertExpectations(suite.T())
	suite.mockClient.AssertExpectations(suite.T())
}

// TestNewProductionGRPCLogger tests the NewProductionGRPCLogger function
func (suite *BufferedGrpcWriteSyncerTestSuite) TestNewProductionGRPCLogger() {
	logger := NewProductionGRPCLogger(suite.grpcSyncer)
	suite.NotNil(logger)

	// Verify logger configuration
	suite.True(logger.Core().Enabled(zapcore.InfoLevel))
	suite.True(logger.Core().Enabled(zapcore.ErrorLevel))
}
