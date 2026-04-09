// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// mockFlowCollector implements FlowCollector for testing.
type mockFlowCollector struct {
	runErr error
}

func (m *mockFlowCollector) Run(ctx context.Context) error {
	if m.runErr != nil {
		return m.runErr
	}

	<-ctx.Done()

	return ctx.Err()
}

func TestFlowCollectorStreamFactory_Name(t *testing.T) {
	factory := &FlowCollectorStreamFactory{}

	name := factory.Name()

	assert.Equal(t, "FlowCollector", name)
}

func TestFlowCollectorStreamFactory_NewStreamClient_Success(t *testing.T) {
	mockCollector := &mockFlowCollector{}
	factory := &FlowCollectorStreamFactory{
		Factory: func(ctx context.Context) (FlowCollector, error) {
			return mockCollector, nil
		},
	}

	client, err := factory.NewStreamClient(context.Background(), nil)

	require.NoError(t, err)
	assert.NotNil(t, client)
}

func TestFlowCollectorStreamFactory_NewStreamClient_Error(t *testing.T) {
	expectedErr := errors.New("factory error")
	factory := &FlowCollectorStreamFactory{
		Factory: func(ctx context.Context) (FlowCollector, error) {
			return nil, expectedErr
		},
	}

	client, err := factory.NewStreamClient(context.Background(), nil)

	require.ErrorIs(t, err, expectedErr)
	assert.Nil(t, client)
}

func TestFlowCollectorStreamFactory_ImplementsInterface(t *testing.T) {
	factory := &FlowCollectorStreamFactory{}

	var _ stream.StreamClientFactory = factory
}

func TestFlowCollectorAdapter_Run(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockCollector := &mockFlowCollector{}
	adapter := &flowCollectorAdapter{collector: mockCollector}

	err := adapter.Run(ctx)

	assert.ErrorIs(t, err, context.Canceled)
}

func TestFlowCollectorAdapter_Run_Error(t *testing.T) {
	expectedErr := errors.New("run error")
	mockCollector := &mockFlowCollector{runErr: expectedErr}
	adapter := &flowCollectorAdapter{collector: mockCollector}

	err := adapter.Run(context.Background())

	assert.ErrorIs(t, err, expectedErr)
}

func TestFlowCollectorAdapter_SendKeepalive(t *testing.T) {
	adapter := &flowCollectorAdapter{}

	err := adapter.SendKeepalive(context.Background())

	assert.NoError(t, err)
}

func TestFlowCollectorAdapter_Close(t *testing.T) {
	adapter := &flowCollectorAdapter{}

	err := adapter.Close()

	assert.NoError(t, err)
}
