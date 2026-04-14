// Copyright 2026 Illumio, Inc. All Rights Reserved.

package falco

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
		Logger: logger,
	}

	client, err := factory.NewCollector(context.Background())

	require.NoError(t, err)
	assert.NotNil(t, client)
}
