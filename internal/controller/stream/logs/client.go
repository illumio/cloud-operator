// Copyright 2026 Illumio, Inc. All Rights Reserved.

package logs

import (
	"context"
	"errors"
	"io"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/illumio/cloud-operator/internal/controller/logging"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// Verify logsClient implements stream.StreamClient.
var _ stream.StreamClient = (*logsClient)(nil)

// logsClient implements stream.StreamClient for the logs stream.
type logsClient struct {
	stream             logging.LogStream
	conn               *grpc.ClientConn
	logger             *zap.Logger
	bufferedGrpcSyncer *logging.BufferedGrpcWriteSyncer
}

// Run receives log responses from the server until the stream closes.
// The actual log sending is handled by BufferedGrpcWriteSyncer.
func (c *logsClient) Run(ctx context.Context) error {
	// Update the grpc syncer with the new stream
	c.bufferedGrpcSyncer.UpdateClient(c.stream, c.conn)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, err := c.stream.Recv()
		if errors.Is(err, io.EOF) {
			c.logger.Info("Server closed the SendLogs stream")

			return nil
		}

		if err != nil {
			c.logger.Error("Logs stream terminated", zap.Error(err))

			return err
		}
	}
}

// SendKeepalive sends a keepalive message on the logs stream.
// Note: Keepalive for logs is handled by BufferedGrpcWriteSyncer, so this is a no-op.
func (c *logsClient) SendKeepalive(_ context.Context) error {
	// Logs keepalive is handled internally by BufferedGrpcWriteSyncer
	return nil
}

// Close is a no-op for logs client.
// Shutdown is handled via context cancellation.
func (c *logsClient) Close() error {
	return nil
}
