// Copyright 2026 Illumio, Inc. All Rights Reserved.

package falco

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/illumio/cloud-operator/internal/controller/collector"
)

const (
	// FalcoPort is the port for the Falco HTTP server.
	FalcoPort = ":5000"

	// HTTP server timeouts.
	httpReadHeaderTimeout = 5 * time.Second
	httpReadTimeout       = 30 * time.Second
	serverRestartDelay    = 5 * time.Second
)

// StartServer starts the Falco HTTP server for receiving Falco events.
func StartServer(ctx context.Context, logger *zap.Logger, falcoEventChan chan string) {
	http.HandleFunc("/", collector.NewFalcoEventHandler(falcoEventChan))

	for {
		select {
		case <-ctx.Done():
			logger.Info("Falco server shutting down")

			return
		default:
		}

		var listenerConfig net.ListenConfig

		listener, err := listenerConfig.Listen(ctx, "tcp", FalcoPort)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				logger.Info("Falco server shutting down")

				return
			}

			logger.Fatal("Failed to listen on Falco port", zap.String("address", FalcoPort), zap.Error(err))
		}

		falcoServer := &http.Server{
			Addr:              FalcoPort,
			ReadHeaderTimeout: httpReadHeaderTimeout,
			ReadTimeout:       httpReadTimeout,
		}

		logger.Info("Falco server listening", zap.String("address", FalcoPort))

		err = falcoServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Falco server failed, restarting", zap.Error(err), zap.Duration("delay", serverRestartDelay))
			time.Sleep(serverRestartDelay)
		}
	}
}
