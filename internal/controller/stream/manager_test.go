// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

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
)

// ManagedStream represents a single managed stream with its factory and configuration.
// Moved here from manager.go since it's only used in tests.
type ManagedStream struct {
	Factory         StreamClientFactory
	KeepalivePeriod time.Duration
	Done            chan struct{}
}

// mockStreamClient mocks the StreamClient interface for testing.
type mockStreamClient struct {
	mock.Mock
}

func (m *mockStreamClient) Run(ctx context.Context) error {
	args := m.Called(ctx)

	return args.Error(0)
}

func (m *mockStreamClient) SendKeepalive(ctx context.Context) error {
	args := m.Called(ctx)

	return args.Error(0)
}

func (m *mockStreamClient) Close() error {
	args := m.Called()

	return args.Error(0)
}

// mockStreamClientFactory mocks the StreamClientFactory interface for testing.
type mockStreamClientFactory struct {
	mock.Mock
}

func (m *mockStreamClientFactory) NewStreamClient(ctx context.Context, conn grpc.ClientConnInterface) (StreamClient, error) {
	args := m.Called(ctx, conn)
	if client, ok := args.Get(0).(StreamClient); ok {
		return client, args.Error(1)
	}

	return nil, args.Error(1)
}

func (m *mockStreamClientFactory) Name() string {
	args := m.Called()

	return args.String(0)
}

func TestRunStreamWithKeepalive_Success(t *testing.T) {
	logger := zap.NewNop()
	mockFactory := &mockStreamClientFactory{}
	mockClient := &mockStreamClient{}

	ctx, cancel := context.WithCancel(context.Background())

	mockFactory.On("NewStreamClient", mock.Anything, mock.Anything).Return(mockClient, nil).Once()
	mockFactory.On("Name").Return("TestStream").Maybe()

	// Run returns after context cancel
	mockClient.On("Run", mock.Anything).Run(func(args mock.Arguments) {
		// Wait for context cancellation
		ctx, ok := args.Get(0).(context.Context)
		if ok {
			<-ctx.Done()
		}
	}).Return(context.Canceled).Once()

	mockClient.On("SendKeepalive", mock.Anything).Return(nil).Maybe()
	mockClient.On("Close").Return(nil).Once()

	// Cancel after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := runStreamWithKeepalive(ctx, logger, nil, mockFactory, 10*time.Millisecond)

	require.ErrorIs(t, err, context.Canceled)
	mockFactory.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestRunStreamWithKeepalive_FactoryError(t *testing.T) {
	logger := zap.NewNop()
	mockFactory := &mockStreamClientFactory{}

	expectedErr := errors.New("factory error")
	mockFactory.On("NewStreamClient", mock.Anything, mock.Anything).Return(nil, expectedErr).Once()
	mockFactory.On("Name").Return("TestStream").Maybe()

	err := runStreamWithKeepalive(context.Background(), logger, nil, mockFactory, 10*time.Millisecond)

	require.ErrorIs(t, err, expectedErr)
	mockFactory.AssertExpectations(t)
}

func TestRunStreamWithKeepalive_RunError(t *testing.T) {
	logger := zap.NewNop()
	mockFactory := &mockStreamClientFactory{}
	mockClient := &mockStreamClient{}

	expectedErr := errors.New("run error")

	mockFactory.On("NewStreamClient", mock.Anything, mock.Anything).Return(mockClient, nil).Once()
	mockFactory.On("Name").Return("TestStream").Maybe()
	mockClient.On("Run", mock.Anything).Return(expectedErr).Once()
	mockClient.On("SendKeepalive", mock.Anything).Return(nil).Maybe()
	mockClient.On("Close").Return(nil).Once()

	err := runStreamWithKeepalive(context.Background(), logger, nil, mockFactory, 100*time.Millisecond)

	require.ErrorIs(t, err, expectedErr)
	mockFactory.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestRunStreamWithKeepalive_KeepaliveError(t *testing.T) {
	logger := zap.NewNop()
	mockFactory := &mockStreamClientFactory{}
	mockClient := &mockStreamClient{}

	mockFactory.On("NewStreamClient", mock.Anything, mock.Anything).Return(mockClient, nil).Once()
	mockFactory.On("Name").Return("TestStream").Maybe()

	// Run blocks until context is canceled
	mockClient.On("Run", mock.Anything).Run(func(args mock.Arguments) {
		ctx, ok := args.Get(0).(context.Context)
		if ok {
			<-ctx.Done()
		}
	}).Return(context.Canceled).Once()

	// First keepalive fails - should cancel the stream context
	keepaliveErr := errors.New("keepalive error")
	mockClient.On("SendKeepalive", mock.Anything).Return(keepaliveErr).Once()
	mockClient.On("Close").Return(nil).Once()

	err := runStreamWithKeepalive(context.Background(), logger, nil, mockFactory, 10*time.Millisecond)

	// Should return context.Canceled because keepalive failure cancels the context
	require.ErrorIs(t, err, context.Canceled)
	mockFactory.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestManageStream_ClosesChannelOnExit(t *testing.T) {
	logger := zap.NewNop()
	mockFactory := &mockStreamClientFactory{}
	mockClient := &mockStreamClient{}

	ctx := t.Context()

	done := make(chan struct{})

	mockFactory.On("NewStreamClient", mock.Anything, mock.Anything).Return(mockClient, nil).Maybe()
	mockFactory.On("Name").Return("TestStream").Maybe()

	// Return ErrStopRetries immediately to exit the retry loop
	mockClient.On("Run", mock.Anything).Return(ErrStopRetries).Once()
	mockClient.On("SendKeepalive", mock.Anything).Return(nil).Maybe()
	mockClient.On("Close").Return(nil).Maybe()

	go ManageStream(
		ctx, logger, nil, mockFactory,
		10*time.Millisecond,
		SuccessPeriods{Connect: 1 * time.Millisecond, Auth: 1 * time.Millisecond},
		done,
	)

	// Wait for done channel to close
	select {
	case <-done:
		// Success - channel was closed
	case <-time.After(5 * time.Second):
		t.Fatal("done channel was not closed")
	}
}

func TestManageStream_StopRetriesError(t *testing.T) {
	logger := zap.NewNop()
	mockFactory := &mockStreamClientFactory{}
	mockClient := &mockStreamClient{}

	done := make(chan struct{})

	mockFactory.On("NewStreamClient", mock.Anything, mock.Anything).Return(mockClient, nil).Maybe()
	mockFactory.On("Name").Return("TestStream").Maybe()

	// Return ErrStopRetries to stop the retry loop
	mockClient.On("Run", mock.Anything).Return(ErrStopRetries).Maybe()
	mockClient.On("SendKeepalive", mock.Anything).Return(nil).Maybe()
	mockClient.On("Close").Return(nil).Maybe()

	go ManageStream(
		context.Background(), logger, nil, mockFactory,
		10*time.Millisecond,
		SuccessPeriods{Connect: 1 * time.Millisecond, Auth: 1 * time.Millisecond},
		done,
	)

	// Wait for done channel to close (should happen when ErrStopRetries is returned)
	select {
	case <-done:
		// Success - channel was closed
	case <-time.After(5 * time.Second):
		t.Fatal("done channel was not closed after ErrStopRetries")
	}
}

func TestManagedStream_Struct(t *testing.T) {
	mockFactory := &mockStreamClientFactory{}
	done := make(chan struct{})

	ms := ManagedStream{
		Factory:         mockFactory,
		KeepalivePeriod: 30 * time.Second,
		Done:            done,
	}

	assert.Equal(t, mockFactory, ms.Factory)
	assert.Equal(t, 30*time.Second, ms.KeepalivePeriod)
	assert.Equal(t, done, ms.Done)
}
