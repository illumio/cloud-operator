// Copyright 2026 Illumio, Inc. All Rights Reserved.

package ovnk

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestFactory_NewCollector(t *testing.T) {
	logger := zap.NewNop()
	factory := &Factory{
		Logger:             logger,
		IPFIXCollectorPort: "4739",
	}

	client, err := factory.NewCollector(context.Background())

	require.NoError(t, err)
	assert.NotNil(t, client)
}

func TestFactory_NewCollector_WithPort(t *testing.T) {
	logger := zap.NewNop()
	factory := &Factory{
		Logger:             logger,
		IPFIXCollectorPort: "9999",
	}

	client, err := factory.NewCollector(context.Background())

	require.NoError(t, err)
	assert.NotNil(t, client)
}
