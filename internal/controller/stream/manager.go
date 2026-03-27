// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	"context"
	"errors"
	"math"
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/auth"
	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/k8sclient"
	"github.com/illumio/cloud-operator/internal/controller/logging"
	"github.com/illumio/cloud-operator/internal/pkg/timeutil"
)

// FlowCollectorDeterminer is a function that determines which flow collector to use.
// It returns the factory for the determined collector, or nil if none available.
type FlowCollectorDeterminer func(ctx context.Context, k8sClient K8sClientGetter) StreamClientFactory

// FactoryConfig holds all factories and configuration needed to run streams.
type FactoryConfig struct {
	// Stream factories
	ConfigFactory       StreamClientFactory
	LogsFactory         StreamClientFactory
	ResourcesFactory    StreamClientFactory
	NetworkFlowsFactory StreamClientFactory

	// Shared components
	BufferedGrpcSyncer *logging.BufferedGrpcWriteSyncer
	FlowCache          *FlowCache
	Stats              *Stats

	// Configuration
	KeepalivePeriods KeepalivePeriods
	SuccessPeriods   SuccessPeriods
	StatsLogPeriod   time.Duration

	// Flow collector determiner (called at runtime to pick Cilium/OVN-K/Falco)
	DetermineFlowCollector FlowCollectorDeterminer
}

// ManagedStream represents a single managed stream with its factory and configuration.
type ManagedStream struct {
	Factory         StreamClientFactory
	KeepalivePeriod time.Duration
	Done            chan struct{}
}

// ManageStream manages a stream with backoff and reconnection logic.
func ManageStream(
	ctx context.Context,
	logger *zap.Logger,
	grpcClient pb.KubernetesInfoServiceClient,
	factory StreamClientFactory,
	keepalivePeriod time.Duration,
	successPeriods SuccessPeriods,
	done chan struct{},
) {
	defer close(done)

	streamLogger := logger.With(zap.String("stream", factory.Name()))

	connectAndStream := func() error {
		return runStreamWithKeepalive(ctx, streamLogger, grpcClient, factory, keepalivePeriod)
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
		streamLogger.Error("Failed to reset connectAndStream. Something is very wrong", zap.Error(err))
	}
}

// runStreamWithKeepalive runs a stream client with periodic keepalives.
func runStreamWithKeepalive(
	ctx context.Context,
	logger *zap.Logger,
	grpcClient pb.KubernetesInfoServiceClient,
	factory StreamClientFactory,
	keepalivePeriod time.Duration,
) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	client, err := factory.NewStreamClient(streamCtx, grpcClient)
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
	envMap EnvironmentConfig,
	factoryConfig FactoryConfig,
	falcoEventChan chan string,
) {
	// Start Falco HTTP server (needed even if not using Falco collector)
	http.HandleFunc("/", collector.NewFalcoEventHandler(falcoEventChan))

	go startFalcoServer(ctx, logger)

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
	envMap EnvironmentConfig,
	factoryConfig FactoryConfig,
) string {
	authCtx, authCancel := context.WithCancel(ctx)
	defer authCancel()

	// Establish authenticated connection
	conn, grpcClient, err := NewAuthenticatedConnection(authCtx, logger, envMap)
	if err != nil {
		logger.Error("Failed to establish initial connection; will retry", zap.Error(err))

		return "Failed to establish initial connection"
	}
	defer conn.Close() //nolint:errcheck

	// Create K8s client
	k8sClient, err := k8sclient.NewClient()
	if err != nil {
		logger.Error("Failed to create Kubernetes client", zap.Error(err))

		return "Failed to create Kubernetes client"
	}

	// Determine flow collector first (needed for resources stream to report correct collector)
	flowCollectorFactory := determineFlowCollector(ctx, logger, k8sClient, factoryConfig)

	// Set K8sClient and FlowCollector on resources factory
	if setter, ok := factoryConfig.ResourcesFactory.(K8sClientSetter); ok {
		setter.SetK8sClient(k8sClient)
	}

	if setter, ok := factoryConfig.ResourcesFactory.(FlowCollectorSetter); ok {
		if flowCollectorFactory != nil {
			// Get the flow collector type from the factory name
			setter.SetFlowCollector(flowCollectorFactory.Name())
		}
	}

	// Set connection on logs factory for BufferedGrpcWriteSyncer
	if setter, ok := factoryConfig.LogsFactory.(ConnSetter); ok {
		setter.SetConn(conn)
	}

	// Start stats logger
	StartStatsLogger(authCtx, logger, factoryConfig.Stats, envMap.StatsLogPeriod)

	// Create done channels for each stream
	configDone := make(chan struct{})
	logsDone := make(chan struct{})
	resourcesDone := make(chan struct{})
	flowCollectorDone := make(chan struct{})
	networkFlowsDone := make(chan struct{})
	flowCacheDone := make(chan struct{})

	// Set log stream done channel for BufferedGrpcSyncer
	factoryConfig.BufferedGrpcSyncer.SetDone(logsDone)

	// Start configuration stream
	go ManageStream(
		authCtx, logger, grpcClient,
		factoryConfig.ConfigFactory,
		factoryConfig.KeepalivePeriods.Configuration,
		factoryConfig.SuccessPeriods,
		configDone,
	)

	// Start logs stream
	go ManageStream(
		authCtx, logger, grpcClient,
		factoryConfig.LogsFactory,
		factoryConfig.KeepalivePeriods.Logs,
		factoryConfig.SuccessPeriods,
		logsDone,
	)

	// Start resources stream
	go ManageStream(
		authCtx, logger, grpcClient,
		factoryConfig.ResourcesFactory,
		factoryConfig.KeepalivePeriods.KubernetesResources,
		factoryConfig.SuccessPeriods,
		resourcesDone,
	)

	// Start flow cache
	go func() {
		defer close(flowCacheDone)

		if err := factoryConfig.FlowCache.Run(authCtx, logger); err != nil {
			logger.Info("Flow cache stopped", zap.Error(err))
		}
	}()

	// Start flow collector
	if flowCollectorFactory != nil {
		go ManageStream(
			authCtx, logger, grpcClient,
			flowCollectorFactory,
			factoryConfig.KeepalivePeriods.KubernetesNetworkFlows,
			factoryConfig.SuccessPeriods,
			flowCollectorDone,
		)
	} else {
		close(flowCollectorDone)
	}

	// Start network flows stream (sends cached flows to CloudSecure)
	go ManageStream(
		authCtx, logger, grpcClient,
		factoryConfig.NetworkFlowsFactory,
		factoryConfig.KeepalivePeriods.KubernetesNetworkFlows,
		factoryConfig.SuccessPeriods,
		networkFlowsDone,
	)

	logger.Info("All streams are open and running")

	// Wait for any stream to close
	select {
	case <-configDone:
		return "Configuration stream closed"
	case <-logsDone:
		return "Log stream closed"
	case <-resourcesDone:
		return "Resource stream closed"
	case <-flowCollectorDone:
		return "Flow collector stream closed"
	case <-networkFlowsDone:
		return "Network flows stream closed"
	case <-flowCacheDone:
		return "Flow cache closed"
	case <-authCtx.Done():
		return "Auth context canceled"
	}
}

// startFalcoServer starts the Falco HTTP server for receiving Falco events.
func startFalcoServer(ctx context.Context, logger *zap.Logger) {
	for {
		var listenerConfig net.ListenConfig

		listener, err := listenerConfig.Listen(ctx, "tcp", FalcoPort)
		if err != nil {
			logger.Fatal("Failed to listen on Falco port", zap.String("address", FalcoPort), zap.Error(err))
		}

		falcoServer := &http.Server{
			Addr:              FalcoPort,
			ReadHeaderTimeout: HTTPReadHeaderTimeout,
			ReadTimeout:       HTTPReadTimeout,
		}

		logger.Info("Falco server listening", zap.String("address", FalcoPort))

		err = falcoServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Falco server failed, restarting", zap.Error(err), zap.Duration("delay", ServerRestartDelay))
			time.Sleep(ServerRestartDelay)
		}
	}
}

// determineFlowCollector determines which flow collector to use based on cluster setup.
func determineFlowCollector(
	ctx context.Context,
	logger *zap.Logger,
	k8sClient k8sclient.Client,
	factoryConfig FactoryConfig,
) StreamClientFactory {
	if factoryConfig.DetermineFlowCollector == nil {
		logger.Warn("No flow collector determiner configured")

		return nil
	}

	factory := factoryConfig.DetermineFlowCollector(ctx, k8sClient)
	if factory != nil {
		logger.Info("Using flow collector", zap.String("collector", factory.Name()))
	} else {
		logger.Warn("No flow collector available")
	}

	return factory
}

// ConnectionInfo holds the gRPC connection and client.
type ConnectionInfo struct {
	Conn       *grpc.ClientConn
	GrpcClient pb.KubernetesInfoServiceClient
}

// NewAuthenticatedConnection gets a valid token and creates a connection to CloudSecure.
func NewAuthenticatedConnection(ctx context.Context, logger *zap.Logger, envMap EnvironmentConfig) (*grpc.ClientConn, pb.KubernetesInfoServiceClient, error) {
	k8sClient, err := k8sclient.NewClient()
	if err != nil {
		return nil, nil, err
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
		return nil, nil, err
	}

	conn, err := auth.SetUpOAuthConnection(ctx, logger, envMap.TokenEndpoint, envMap.TlsSkipVerify, clientID, clientSecret)
	if err != nil {
		logger.Error("Failed to set up an OAuth connection", zap.Error(err))

		return nil, nil, err
	}

	client := pb.NewKubernetesInfoServiceClient(conn)

	return conn, client, err
}
