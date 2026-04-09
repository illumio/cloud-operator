// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

func TestFactory_Name(t *testing.T) {
	factory := &Factory{}

	name := factory.Name()

	assert.Equal(t, "SendKubernetesResources", name)
}

func TestFactory_FlowCollectorType(t *testing.T) {
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
			factory := &Factory{
				FlowCollectorType: tc.collector,
			}

			assert.Equal(t, tc.collector, factory.FlowCollectorType)
		})
	}
}

func TestFactory_ImplementsInterface(t *testing.T) {
	factory := &Factory{}

	var _ stream.StreamClientFactory = factory
}
