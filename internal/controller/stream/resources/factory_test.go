// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"k8s.io/client-go/kubernetes"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// mockK8sClientGetter mocks stream.K8sClientGetter for testing.
type mockK8sClientGetter struct {
	mock.Mock
}

func (m *mockK8sClientGetter) GetClientset() kubernetes.Interface {
	args := m.Called()
	if clientset, ok := args.Get(0).(kubernetes.Interface); ok {
		return clientset
	}

	return nil
}

func TestFactory_Name(t *testing.T) {
	factory := &Factory{}

	name := factory.Name()

	assert.Equal(t, "SendKubernetesResources", name)
}

func TestFactory_SetFlowCollector(t *testing.T) {
	tests := []struct {
		name      string
		collector pb.FlowCollector
	}{
		{"Cilium", pb.FlowCollector_FLOW_COLLECTOR_CILIUM},
		{"OVNK", pb.FlowCollector_FLOW_COLLECTOR_OVNK},
		{"Falco", pb.FlowCollector_FLOW_COLLECTOR_FALCO},
		{"Unspecified", pb.FlowCollector_FLOW_COLLECTOR_UNSPECIFIED},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			factory := &Factory{}

			factory.SetFlowCollector(tc.collector)

			assert.Equal(t, tc.collector, factory.FlowCollector)
		})
	}
}

func TestFactory_SetK8sClient(t *testing.T) {
	factory := &Factory{}

	// SetK8sClient expects a k8sclient.Client, which we can't easily mock
	// So we test that it doesn't panic with a non-matching type
	mockClient := &mockK8sClientGetter{}
	factory.SetK8sClient(mockClient)

	// The mock doesn't implement k8sclient.Client, so K8sClient should remain nil
	assert.Nil(t, factory.K8sClient)
}

func TestFactory_ImplementsInterfaces(t *testing.T) {
	factory := &Factory{}

	// Verify factory implements the required interfaces
	var _ stream.StreamClientFactory = factory

	var _ stream.K8sClientSetter = factory

	var _ stream.FlowCollectorSetter = factory
}
