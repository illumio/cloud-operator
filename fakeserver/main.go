// Copyright 2024 Illumio, Inc. All Rights Reserved.

package main

import (
	"context"
	"flag"
	stdlog "log" // Using standard log for ProxyServer, can be replaced with zap
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"go.uber.org/zap"
	// Ensure FakeServer, ProxyServer, AuthService structs are defined in other files in this package.
)

func main() {
	proxyFlag := flag.Bool("proxy", false, "Start the proxy server")
	flag.Parse()

	// Initialize structured logger (Zap)
	appLogger, err := zap.NewDevelopment() // In production, consider zap.NewProduction()
	if err != nil {
		stdlog.Fatalf("Failed to initialize zap logger: %v", err) // Use standard log if zap fails
	}
	defer func() {
		// Sync flushes any buffered log entries before application exits.
		_ = appLogger.Sync()
	}()
	appLogger.Info("Application starting...")

	// JWT Token Generation (for FakeServer's gRPC authentication)
	// The AuthService will be configured to issue this exact token.
	grpcAudience := "192.168.65.254:50051"    // Audience for the token (FakeServer gRPC address)
	tokenSubject := "fakeserver-client-token" // Subject or other identifier for the token
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": tokenSubject,
		"aud": []string{grpcAudience},
		"exp": time.Now().Add(time.Hour * 72).Unix(), // Expiration time
	})
	// IMPORTANT: Use a strong, securely managed key in production!
	// This key must be known to whatever would validate this token if it were real.
	// For this setup, it's just used to sign a token that the FakeServer's auth interceptor expects.
	jwtSigningKey := []byte("your-very-secret-and-strong-key-for-hs256")
	signedGRPCToken, err := jwtToken.SignedString(jwtSigningKey)
	if err != nil {
		appLogger.Fatal("JWT token for gRPC could not be signed", zap.Error(err))
	}
	appLogger.Info("JWT token for FakeServer gRPC prepared.")

	// Initialize FakeServer
	fakeServer := &FakeServer{
		address:     "0.0.0.0:50051", // gRPC address for FakeServer
		httpAddress: "0.0.0.0:50053", // HTTP address for FakeServer's AuthService
		// stopChan is removed; shutdown managed via Stop method and context
		token:  signedGRPCToken, // This is the token the gRPC interceptor will expect
		logger: appLogger.Named("FakeServer"),
		state:  &ServerState{}, // Initialize FakeServer state
		// authService will be initialized within FakeServer.Start or NewFakeServer
	}

	// Start FakeServer
	if err := fakeServer.Start(); err != nil { // FakeServer.Start() is from the refactored code
		appLogger.Fatal("Failed to start FakeServer", zap.Error(err))
	}
	appLogger.Info("FakeServer started successfully.")

	// Initialize and start ProxyServer if flag is set
	var proxy *ProxyServer
	if *proxyFlag {
		// ProxyServer can use a different logger or a sub-logger from the appLogger
		proxyLogger := stdlog.New(os.Stdout, "[ProxyServer] ", stdlog.LstdFlags|stdlog.Lmicroseconds)
		// proxyLogger := appLogger.Named("ProxyServer") // If ProxyServer is adapted for Zap

		proxy, err = NewProxyServer("0.0.0.0:8888", "0.0.0.0:50052", proxyLogger)
		if err != nil {
			appLogger.Fatal("Failed to initialize ProxyServer", zap.Error(err))
		}

		appLogger.Info("Starting ProxyServer...")
		if err := proxy.Start(); err != nil { // ProxyServer.Start() from refactored code
			appLogger.Fatal("Failed to start ProxyServer", zap.Error(err))
		}
		appLogger.Info("ProxyServer started successfully.")
	}

	// Setup OS signal catching for graceful shutdown
	osSignalChan := make(chan os.Signal, 1)
	signal.Notify(osSignalChan, syscall.SIGINT, syscall.SIGTERM)

	// Block until a shutdown signal is received
	receivedSignal := <-osSignalChan
	appLogger.Info("Received OS signal, initiating shutdown...", zap.String("signal", receivedSignal.String()))

	// Graceful shutdown sequence
	// Create a context with a timeout for the entire shutdown process.
	// Individual Stop() methods might have their own shorter timeouts for specific components.
	overallShutdownCtx, overallShutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer overallShutdownCancel()

	var shutdownWg sync.WaitGroup

	// Initiate ProxyServer shutdown if it was started
	if proxy != nil {
		shutdownWg.Add(1)
		go func() {
			defer shutdownWg.Done()
			appLogger.Info("Attempting to stop ProxyServer...")
			// Pass a context derived from the overall shutdown context for ProxyServer's stop
			proxyStopCtx, proxyStopCancel := context.WithTimeout(overallShutdownCtx, 25*time.Second) // Proxy gets most of the time
			defer proxyStopCancel()
			if stopErr := proxy.Stop(proxyStopCtx); stopErr != nil {
				appLogger.Error("Error during ProxyServer stop", zap.Error(stopErr))
			} else {
				appLogger.Info("ProxyServer stopped successfully.")
			}
		}()
	}

	// Initiate FakeServer shutdown
	shutdownWg.Add(1)
	go func() {
		defer shutdownWg.Done()
		appLogger.Info("Attempting to stop FakeServer...")
		// Pass a context derived from the overall shutdown context for FakeServer's stop
		fakeServerStopCtx, fakeServerStopCancel := context.WithTimeout(overallShutdownCtx, 25*time.Second) // FakeServer also gets time
		defer fakeServerStopCancel()
		if stopErr := fakeServer.Stop(fakeServerStopCtx); stopErr != nil {
			appLogger.Error("Error during FakeServer stop", zap.Error(stopErr))
		} else {
			appLogger.Info("FakeServer stopped successfully.")
		}
	}()

	// Wait for all server components to shut down
	appLogger.Info("Waiting for all services to shut down...")
	shutdownCompleteChan := make(chan struct{})
	go func() {
		shutdownWg.Wait()
		close(shutdownCompleteChan)
	}()

	select {
	case <-shutdownCompleteChan:
		appLogger.Info("All services shut down gracefully.")
	case <-overallShutdownCtx.Done(): // This triggers if the 30s overall timeout is hit
		appLogger.Error("Graceful shutdown timed out. Some services might not have stopped cleanly.", zap.Error(overallShutdownCtx.Err()))
	}

	appLogger.Info("Application exiting.")
}
