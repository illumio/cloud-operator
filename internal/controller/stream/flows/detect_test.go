// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	runtimescheme "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// fakeK8sClientGetter implements collector.K8sClientGetter for testing detection.
type fakeK8sClientGetter struct {
	clientset kubernetes.Interface
	dynamic   dynamic.Interface
	discovery discovery.DiscoveryInterface
}

func (f *fakeK8sClientGetter) GetClientset() kubernetes.Interface  { return f.clientset }
func (f *fakeK8sClientGetter) GetDynamicClient() dynamic.Interface { return f.dynamic }
func (f *fakeK8sClientGetter) GetDiscoveryClient() discovery.DiscoveryInterface {
	return f.discovery
}

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

	require.NoError(t, err)
}

func TestFlowCollectorAdapter_Close(t *testing.T) {
	adapter := &flowCollectorAdapter{}

	err := adapter.Close()

	require.NoError(t, err)
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

// TestDetectFlowCollector_NoSupportedCNI verifies that when no supported CNI is
// detected the operator does NOT fall back to any collector: it reports
// FLOW_COLLECTOR_DISABLED and returns a nil factory so main.go skips registering
// a flow-collector stream.
func TestDetectFlowCollector_NoSupportedCNI(t *testing.T) {
	// Empty clientset: no Hubble Relay service (Cilium unavailable), no OVN-K
	// namespace, no aws-node pods (AWS VPC CNI unavailable) -> falls through.
	getter := &fakeK8sClientGetter{
		clientset: k8sfake.NewClientset(),
		dynamic:   dynamicfake.NewSimpleDynamicClient(runtimescheme.NewScheme()),
	}

	flowCollectorType, name, factory := DetectFlowCollector(context.Background(), CollectorConfig{
		Logger:           zap.NewNop(),
		K8sClient:        getter,
		CiliumNamespaces: []string{"kube-system"},
		OVNKNamespace:    "openshift-ovn-kubernetes",
	})

	assert.Equal(t, pb.FlowCollector_FLOW_COLLECTOR_DISABLED, flowCollectorType,
		"no supported CNI should report FLOW_COLLECTOR_DISABLED")
	assert.Equal(t, "None", name)
	assert.Nil(t, factory, "no supported CNI should return a nil factory (no fallback)")
}

// Note: the positive DetectFlowCollector paths (Cilium/OVN-K/AWS VPC CNI) are
// harder to unit test as they require a fake Hubble Relay connection and
// resource fixtures. See collector/cilium_test.go and collector/ovnk_test.go
// for unit tests of the individual detection helpers.
