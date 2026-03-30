// Copyright 2026 Illumio, Inc. All Rights Reserved.

package logs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
)

func TestFactory_Name(t *testing.T) {
	factory := &Factory{}

	name := factory.Name()

	assert.Equal(t, "SendLogs", name)
}

func TestFactory_SetConn(t *testing.T) {
	factory := &Factory{}

	// SetConn should accept a *grpc.ClientConn
	var conn *grpc.ClientConn
	factory.SetConn(conn)

	assert.Equal(t, conn, factory.Conn)
}

func TestFactory_SetConn_WrongType(t *testing.T) {
	factory := &Factory{}

	// SetConn with wrong type should be ignored
	factory.SetConn("not a connection")

	assert.Nil(t, factory.Conn)
}
