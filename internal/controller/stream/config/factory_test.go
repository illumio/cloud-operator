// Copyright 2026 Illumio, Inc. All Rights Reserved.

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFactory_Name(t *testing.T) {
	factory := &Factory{}

	name := factory.Name()

	assert.Equal(t, "GetConfigurationUpdates", name)
}
