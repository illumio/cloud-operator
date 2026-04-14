// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	"context"
	"errors"
	"math"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/illumio/cloud-operator/internal/controller/auth"
	"github.com/illumio/cloud-operator/internal/controller/k8sclient"
	"github.com/illumio/cloud-operator/internal/pkg/timeutil"
)

// SuccessPeriods defines how long a stream must be active to be considered successful.
type SuccessPeriods struct {
	Auth    time.Duration
	Connect time.Duration
}

// ManagedFactory pairs a factory with its keepalive period.
type ManagedFactory struct {
	Factory         StreamClientFactory
	KeepalivePeriod time.Duration
}

// FactoryConfig holds all factories and configuration needed to run streams.
type FactoryConfig struct {
	// Stream factories - manager is oblivious to specific implementations
	Factories []ManagedFactory

	// Shared components
	Stats *Stats

	// Configuration
	SuccessPeriods SuccessPeriods
	StatsLogPeriod time.Duration
}

// ManageStream manages a stream with backoff and reconnection logic.
func ManageStream(
	ctx context.Context,
	logger *zap.Logger,
	conn grpc.ClientConnInterface,
	factory StreamClientFactory,
	keepalivePeriod time.Duration,
	successPeriods SuccessPeriods,
	done chan struct{},
) {
	defer close(done)

	streamLogger := logger.With(zap.String("stream", factory.Name()))

	connectAndStream := func() error {
		select {
		case <-ctx.Done():
			return ErrStopRetries
		default:
			return runStreamWithKeepalive(ctx, streamLogger, conn, factory, keepalivePeriod)
		}
	}

	funcWithBackoff := func() error {
		return exponentialBackoff(backoffOpts{
			InitialBackoff:              StreamInitialBackoff,
			MaxBackoff:                  StreamMaxBackoff,
			MaxJitterPct:                StreamMaxJitterPct,
			SevereErrorThreshold:        StreamSevereErrorThreshold,
			ExponentialFactor:           StreamExponentialFactor,
			Logger:                      streamLogger.With(zap.String("name", "retry_connect_and_stream")),
			ActionTimeToConsiderSuccess: successPeriods.Connect,
		}, connectAndStream)
	}

	funcWithBackoffAndReset := func() error {
		return exponentialBackoff(backoffOpts{
			InitialBackoff:              ResetInitialBackoff,
			MaxBackoff:                  ResetMaxBackoff,
			MaxJitterPct:                ResetMaxJitterPct,
			SevereErrorThreshold:        math.MaxInt,
			ExponentialFactor:           1,
			Logger:                      streamLogger.With(zap.String("name", "reset_retry_connect_and_stream")),
			ActionTimeToConsiderSuccess: successPeriods.Auth,
		}, funcWithBackoff)
	}

	err := funcWithBackoffAndReset()
	if err != nil {
		if errors.Is(err, ErrStopRetries) {
			streamLogger.Info("Stream stopped retrying", zap.Error(err))

			return
		}

		streamLogger.Error("Failed to reset connectAndStream. Something is very wrong", zap.Error(err))
	}
}

// runStreamWithKeepalive runs a stream client with periodic keepalives.
func runStreamWithKeepalive(
	ctx context.Context,
	logger *zap.Logger,
	conn grpc.ClientConnInterface,
	factory StreamClientFactory,
	keepalivePeriod time.Duration,
) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	client, err := factory.NewStreamClient(streamCtx, conn)
	if err != nil {
		logger.Error("Failed to create stream client", zap.Error(err))

		return err
	}
	defer client.Close() //nolint:errcheck

	// Start keepalive goroutine
	go func() {
		ticker := time.NewTicker(timeutil.JitterTime(keepalivePeriod, 0.10))
		defer ticker.Stop()

		for {
			select {
			case <-streamCtx.Done():
				return
			case <-ticker.C:
				if err := client.SendKeepalive(streamCtx); err != nil {
					logger.Debug("Keepalive failed", zap.Error(err))
					cancel()

					return
				}
			}
		}
	}()

	// Run the stream
	return client.Run(streamCtx)
}

// ConnectStreams orchestrates all streams using the factory pattern.
func ConnectStreams(
	ctx context.Context,
	logger *zap.Logger,
	envMap Config,
	factoryConfig FactoryConfig,
) {
	resetTimer := time.NewTimer(timeutil.JitterTime(ConnectionRetryInterval, ConnectionRetryJitter))
	attempt := 0

	for {
		var failureReason string

		attempt++
		logger.Debug("Trying to authenticate and open streams", zap.Int("attempt", attempt))

		select {
		case <-ctx.Done():
			logger.Warn("Context canceled while trying to authenticate and open streams")

			return

		case <-resetTimer.C:
			failureReason = runStreamsOnce(ctx, logger, envMap, factoryConfig)
		}

		logger.Warn("One or more streams have been closed; closing and reopening the connection to CloudSecure",
			zap.String("failureReason", failureReason),
			zap.Int("attempt", attempt),
		)
		resetTimer.Reset(timeutil.JitterTime(ConnectionRetryInterval, ConnectionRetryJitter))
	}
}

// runStreamsOnce establishes connection and runs all streams until one fails.
// Returns the failure reason.
func runStreamsOnce(
	ctx context.Context,
	logger *zap.Logger,
	envMap Config,
	factoryConfig FactoryConfig,
) string {
	authCtx, authCancel := context.WithCancel(ctx)
	defer authCancel()

	// Establish authenticated connection
	conn, err := NewAuthenticatedConnection(authCtx, logger, envMap)
	if err != nil {
		logger.Error("Failed to establish initial connection; will retry", zap.Error(err))

		return "Failed to establish initial connection"
	}
	defer conn.Close() //nolint:errcheck

	// Start stats logger
	StartStatsLogger(authCtx, logger, factoryConfig.Stats, factoryConfig.StatsLogPeriod)

	// Start all stream factories
	doneChannels := make(map[string]chan struct{})

	for _, mf := range factoryConfig.Factories {
		if mf.Factory == nil {
			continue
		}

		done := make(chan struct{})
		doneChannels[mf.Factory.Name()] = done

		go ManageStream(
			authCtx, logger, conn,
			mf.Factory,
			mf.KeepalivePeriod,
			factoryConfig.SuccessPeriods,
			done,
		)
	}

	logger.Info("All streams are open and running")

	// Create a merged channel to wait for any stream to close
	merged := make(chan string, 1)

	for name, done := range doneChannels {
		go func(streamName string, ch chan struct{}) {
			<-ch

			select {
			case merged <- streamName + " stream closed":
			default:
			}
		}(name, done)
	}

	go func() {
		<-authCtx.Done()

		select {
		case merged <- "Auth context canceled":
		default:
		}
	}()

	return <-merged
}

// NewAuthenticatedConnection gets a valid token and creates a connection to CloudSecure.
func NewAuthenticatedConnection(ctx context.Context, logger *zap.Logger, envMap Config) (*grpc.ClientConn, error) {
	k8sClient, err := k8sclient.NewClient()
	if err != nil {
		return nil, err
	}

	clientset := k8sClient.GetClientset()

	authConfig := auth.AuthConfig{
		ClusterCreds:           envMap.ClusterCreds,
		PodNamespace:           envMap.PodNamespace,
		OnboardingClientID:     envMap.OnboardingClientID,
		OnboardingClientSecret: envMap.OnboardingClientSecret,
		OnboardingEndpoint:     envMap.OnboardingEndpoint,
		TlsSkipVerify:          envMap.TlsSkipVerify,
	}

	clientID, clientSecret, err := auth.GetClusterCredentials(ctx, logger, clientset, authConfig)
	if err != nil {
		return nil, err
	}

	conn, err := auth.SetUpOAuthConnection(ctx, logger, envMap.TokenEndpoint, envMap.TlsSkipVerify, clientID, clientSecret)
	if err != nil {
		logger.Error("Failed to set up an OAuth connection", zap.Error(err))

		return nil, err
	}

	return conn, nil
}
