// Copyright 2026 Illumio, Inc. All Rights Reserved.

package ovnk

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestFactory_NewFlowCollector(t *testing.T) {
	logger := zap.NewNop()
	factory := &Factory{
		Logger:             logger,
		IPFIXCollectorPort: "4739",
	}

	client, err := factory.NewFlowCollector(context.Background())

	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, logger, client.logger)
	assert.Equal(t, "4739", client.ipfixCollectorPort)
}

func TestFactory_NewFlowCollector_WithFlowSink(t *testing.T) {
	logger := zap.NewNop()
	factory := &Factory{
		Logger:             logger,
		IPFIXCollectorPort: "9999",
	}

	client, err := factory.NewFlowCollector(context.Background())

	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, "9999", client.ipfixCollectorPort)
}
