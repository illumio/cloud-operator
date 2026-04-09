// Copyright 2026 Illumio, Inc. All Rights Reserved.

package cilium

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

func TestFactory_NewFlowCollector(t *testing.T) {
	logger := zap.NewNop()
	tlsProps := &tls.AuthProperties{}
	factory := &Factory{
		Logger:           logger,
		CiliumNamespaces: []string{"cilium"},
		TlsAuthProps:     tlsProps,
	}

	client, err := factory.NewFlowCollector(context.Background())

	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, logger, client.logger)
	assert.Equal(t, []string{"cilium"}, client.ciliumNamespaces)
	assert.Equal(t, tlsProps, client.tlsAuthProps)
}

func TestFactory_NewFlowCollector_WithFlowSink(t *testing.T) {
	logger := zap.NewNop()
	factory := &Factory{
		Logger:           logger,
		CiliumNamespaces: []string{"kube-system", "cilium"},
	}

	client, err := factory.NewFlowCollector(context.Background())

	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, []string{"kube-system", "cilium"}, client.ciliumNamespaces)
}
