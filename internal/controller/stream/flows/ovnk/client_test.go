// Copyright 2026 Illumio, Inc. All Rights Reserved.

package ovnk

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOVNKClient_SendKeepalive_NoOp(t *testing.T) {
	client := &ovnkClient{}

	err := client.SendKeepalive(context.TODO())

	require.NoError(t, err)
}

func TestOVNKClient_Close(t *testing.T) {
	client := &ovnkClient{}

	err := client.Close()

	require.NoError(t, err)
}

func TestOVNKClient_Close_Idempotent(t *testing.T) {
	client := &ovnkClient{}

	err := client.Close()
	require.NoError(t, err)

	err = client.Close()
	require.NoError(t, err)
}
