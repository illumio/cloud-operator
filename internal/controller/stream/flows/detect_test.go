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

// mockCollector implements Collector for testing.
type mockCollector struct {
	runErr error
}

func (m *mockCollector) Run(ctx context.Context) error {
	if m.runErr != nil {
		return m.runErr
	}

	<-ctx.Done()

	return ctx.Err()
}

// mockCollectorFactory implements CollectorFactory for testing.
type mockCollectorFactory struct {
	collector Collector
	err       error
}

func (m *mockCollectorFactory) NewCollector(_ context.Context) (Collector, error) {
	return m.collector, m.err
}

func TestFlowCollectorStreamFactory_Name(t *testing.T) {
	factory := &FlowCollectorStreamFactory{}

	name := factory.Name()

	assert.Equal(t, "FlowCollector", name)
}

func TestFlowCollectorStreamFactory_Name_WithCollectorName(t *testing.T) {
	factory := &FlowCollectorStreamFactory{CollectorName: "Cilium"}

	name := factory.Name()

	assert.Equal(t, "FlowCollector-Cilium", name)
}

func TestFlowCollectorStreamFactory_NewStreamClient_Success(t *testing.T) {
	mockColl := &mockCollector{}
	factory := &FlowCollectorStreamFactory{
		Factory: &mockCollectorFactory{collector: mockColl},
	}

	client, err := factory.NewStreamClient(context.Background(), nil)

	require.NoError(t, err)
	assert.NotNil(t, client)
}

func TestFlowCollectorStreamFactory_NewStreamClient_Error(t *testing.T) {
	expectedErr := errors.New("factory error")
	factory := &FlowCollectorStreamFactory{
		Factory: &mockCollectorFactory{err: expectedErr},
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

	mockColl := &mockCollector{}
	adapter := &flowCollectorAdapter{collector: mockColl}

	err := adapter.Run(ctx)

	assert.ErrorIs(t, err, context.Canceled)
}

func TestFlowCollectorAdapter_Run_Error(t *testing.T) {
	expectedErr := errors.New("run error")
	mockColl := &mockCollector{runErr: expectedErr}
	adapter := &flowCollectorAdapter{collector: mockColl}

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

func TestCollectorFactoryFunc(t *testing.T) {
	t.Run("wraps function and returns collector", func(t *testing.T) {
		expectedColl := &mockCollector{}
		fn := collectorFactoryFunc(func(ctx context.Context) (Collector, error) {
			return expectedColl, nil
		})

		coll, err := fn.NewCollector(context.Background())

		require.NoError(t, err)
		assert.Equal(t, expectedColl, coll)
	})

	t.Run("wraps function and returns error", func(t *testing.T) {
		expectedErr := errors.New("creation error")
		fn := collectorFactoryFunc(func(ctx context.Context) (Collector, error) {
			return nil, expectedErr
		})

		coll, err := fn.NewCollector(context.Background())

		require.ErrorIs(t, err, expectedErr)
		assert.Nil(t, coll)
	})
}

// Note: DetectFlowCollector tests are more complex as they require:
// - Fake K8s clientset with specific namespaces/resources
// - Mocking IsCiliumAvailable (requires Hubble relay connection)
// - Mocking IsOVNKDeployed (simpler, just checks namespace)
// Full detection logic tests should inject the detection functions
// or use integration tests. See collector/cilium_test.go and
// collector/ovnk_test.go for unit tests of the detection helpers.
