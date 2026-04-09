// Copyright 2026 Illumio, Inc. All Rights Reserved.

package falco

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestFactory_NewFlowCollector(t *testing.T) {
	logger := zap.NewNop()
	falcoEventChan := make(chan string, 10)
	factory := &Factory{
		Logger:         logger,
		FalcoEventChan: falcoEventChan,
	}

	client, err := factory.NewFlowCollector(context.Background())

	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, logger, client.logger)
	assert.Equal(t, falcoEventChan, client.falcoEventChan)
}

func TestFactory_NewFlowCollector_WithFlowSink(t *testing.T) {
	logger := zap.NewNop()
	falcoEventChan := make(chan string, 10)
	factory := &Factory{
		Logger:         logger,
		FalcoEventChan: falcoEventChan,
	}

	client, err := factory.NewFlowCollector(context.Background())

	require.NoError(t, err)
	assert.NotNil(t, client)
}
