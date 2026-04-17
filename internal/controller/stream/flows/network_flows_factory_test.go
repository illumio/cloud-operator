// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/illumio/cloud-operator/internal/controller/stream"
)

func TestNetworkFlowsFactory_Name(t *testing.T) {
	factory := &NetworkFlowsFactory{}

	name := factory.Name()

	assert.Equal(t, "SendKubernetesNetworkFlows", name)
}

func TestNetworkFlowsFactory_ImplementsInterface(t *testing.T) {
	factory := &NetworkFlowsFactory{}

	var _ stream.StreamClientFactory = factory
}
