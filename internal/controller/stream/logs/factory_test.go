// Copyright 2026 Illumio, Inc. All Rights Reserved.

package logs

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/illumio/cloud-operator/internal/controller/stream"
)

func TestFactory_Name(t *testing.T) {
	factory := &Factory{}

	name := factory.Name()

	assert.Equal(t, "SendLogs", name)
}

func TestFactory_ImplementsInterface(t *testing.T) {
	factory := &Factory{}

	var _ stream.StreamClientFactory = factory
}
