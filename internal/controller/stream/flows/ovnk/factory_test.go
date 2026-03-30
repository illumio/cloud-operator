// Copyright 2026 Illumio, Inc. All Rights Reserved.

package ovnk

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/illumio/cloud-operator/internal/controller/stream"
)

func TestFactory_Name(t *testing.T) {
	factory := &Factory{}

	name := factory.Name()

	assert.Equal(t, "OVNKFlowCollector", name)
}

func TestFactory_NewStreamClient(t *testing.T) {
	factory := &Factory{}

	// NewStreamClient for OVN-K doesn't use the grpcClient
	client, err := factory.NewStreamClient(context.Background(), nil)

	require.NoError(t, err)
	assert.NotNil(t, client)

	// Verify it's an ovnkClient
	_, ok := client.(*ovnkClient)
	assert.True(t, ok)
}

func TestFactory_ImplementsInterface(t *testing.T) {
	factory := &Factory{}

	var _ stream.StreamClientFactory = factory
}
