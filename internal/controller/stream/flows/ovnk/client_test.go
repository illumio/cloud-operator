// Copyright 2026 Illumio, Inc. All Rights Reserved.

package ovnk

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestOVNKClient_Fields(t *testing.T) {
	logger := zap.NewNop()
	client := &ovnkClient{
		logger:             logger,
		ipfixCollectorPort: "4739",
	}

	assert.Equal(t, logger, client.logger)
	assert.Equal(t, "4739", client.ipfixCollectorPort)
}
