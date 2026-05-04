// Copyright 2026 Illumio, Inc. All Rights Reserved.

package falco

import (
	"context"
	"errors"
	"net"
	"net/http"
	"syscall"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/illumio/cloud-operator/internal/controller/collector"
)

const (
	// FalcoPort is the port for the Falco HTTP server.
	FalcoPort = ":5000"

	// HTTP server timeouts.
	httpReadHeaderTimeout = 5 * time.Second
	httpReadTimeout       = 5 * time.Second
)

// StartServer starts the Falco HTTP server for receiving Falco events.
// Returns the server so it can be shutdown when the caller is done.
func StartServer(ctx context.Context, logger *zap.Logger, falcoEventChan chan string) *http.Server {
	// Use dedicated ServeMux to avoid sharing routes with health server
	mux := http.NewServeMux()
	mux.HandleFunc("/", collector.NewFalcoEventHandler(falcoEventChan))

	server := &http.Server{
		Addr:              FalcoPort,
		Handler:           mux,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
	}

	go func() {
		defer close(falcoEventChan)

		listenerConfig := net.ListenConfig{
			Control: func(network, address string, c syscall.RawConn) error {
				return c.Control(func(fd uintptr) {
					// SO_REUSEADDR allows immediate reusing the port after it has been closed.
					err := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
					if err != nil {
						logger.Fatal("Failed to set SO_REUSEADDR socket option on Falco server socket", zap.Error(err))
					}
				})
			},
		}

		listener, err := listenerConfig.Listen(ctx, "tcp", FalcoPort)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				logger.Info("Falco server shutting down")

				return
			}

			logger.Fatal("Failed to listen on Falco port", zap.String("address", FalcoPort), zap.Error(err))
		}

		logger.Info("Falco server listening", zap.String("address", FalcoPort))

		err = server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Falco server stopped", zap.Error(err))
		}
	}()

	return server
}
