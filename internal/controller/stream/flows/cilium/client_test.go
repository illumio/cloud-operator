// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cilium

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/illumio/cloud-operator/internal/controller/hubble"
)

func TestShouldStopRetries_HubbleNotFound(t *testing.T) {
	err := hubble.ErrHubbleNotFound

	result := ShouldStopRetries(err)

	assert.True(t, result)
}

func TestShouldStopRetries_NoPortsAvailable(t *testing.T) {
	err := hubble.ErrNoPortsAvailable

	result := ShouldStopRetries(err)

	assert.True(t, result)
}

func TestShouldStopRetries_WrappedHubbleNotFound(t *testing.T) {
	err := errors.Join(errors.New("wrapper"), hubble.ErrHubbleNotFound)

	result := ShouldStopRetries(err)

	assert.True(t, result)
}

func TestShouldStopRetries_WrappedNoPortsAvailable(t *testing.T) {
	err := errors.Join(errors.New("wrapper"), hubble.ErrNoPortsAvailable)

	result := ShouldStopRetries(err)

	assert.True(t, result)
}

func TestShouldStopRetries_OtherError(t *testing.T) {
	err := errors.New("some other error")

	result := ShouldStopRetries(err)

	assert.False(t, result)
}

func TestShouldStopRetries_NilError(t *testing.T) {
	result := ShouldStopRetries(nil)

	assert.False(t, result)
}

func TestCiliumClient_SendKeepalive_NoOp(t *testing.T) {
	client := &ciliumClient{}

	err := client.SendKeepalive(context.TODO())

	require.NoError(t, err)
}

func TestCiliumClient_Close(t *testing.T) {
	client := &ciliumClient{}

	err := client.Close()

	require.NoError(t, err)
}

func TestCiliumClient_Close_Idempotent(t *testing.T) {
	client := &ciliumClient{}

	err := client.Close()
	require.NoError(t, err)

	err = client.Close()
	require.NoError(t, err)
}
