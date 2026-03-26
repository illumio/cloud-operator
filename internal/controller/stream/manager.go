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
	"k8s.io/client-go/kubernetes"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/auth"
	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/k8sclient"
	"github.com/illumio/cloud-operator/internal/controller/logging"
	"github.com/illumio/cloud-operator/internal/pkg/timeutil"
)

// ManageStream manages any stream with backoff and reconnection logic.
func (sm *Manager) ManageStream(
	logger *zap.Logger,
	connectAndStream func(*zap.Logger, time.Duration) error,
	done chan struct{},
	keepalivePeriod time.Duration,
	successPeriods SuccessPeriods,
) {
	defer close(done)

	f := func() error {
		return connectAndStream(logger, keepalivePeriod)
	}

	funcWithBackoff := func() error {
		return exponentialBackoff(backoffOpts{
			InitialBackoff:       StreamInitialBackoff,
			MaxBackoff:           StreamMaxBackoff,
			MaxJitterPct:         StreamMaxJitterPct,
			SevereErrorThreshold: StreamSevereErrorThreshold,
			ExponentialFactor:    StreamExponentialFactor,
			Logger: logger.With(
				zap.String("name", "retry_connect_and_stream"),
			),
			ActionTimeToConsiderSuccess: successPeriods.Connect,
		}, f)
	}

	funcWithBackoffAndReset := func() error {
		return exponentialBackoff(backoffOpts{
			InitialBackoff:       ResetInitialBackoff,
			MaxBackoff:           ResetMaxBackoff,
			MaxJitterPct:         ResetMaxJitterPct,
			SevereErrorThreshold: math.MaxInt,
			ExponentialFactor:    1,
			Logger: logger.With(
				zap.String("name", "reset_retry_connect_and_stream"),
			),
			ActionTimeToConsiderSuccess: successPeriods.Auth,
		}, funcWithBackoff)
	}

	err := funcWithBackoffAndReset()
	if err != nil {
		logger.Error("Failed to reset connectAndStream. Something is very wrong", zap.Error(err))

		return
	}
}

// StreamFuncs contains the functions to start each stream type.
// These are passed from the caller to avoid circular dependencies.
type StreamFuncs struct {
	Resources func(sm *Manager, logger *zap.Logger, keepalivePeriod time.Duration) error
	Logs      func(sm *Manager, logger *zap.Logger, keepalivePeriod time.Duration) error
	Config    func(sm *Manager, logger *zap.Logger, keepalivePeriod time.Duration) error
	// DetermineFlowCollector returns the flow collector type, stream function, and done channel
	DetermineFlowCollector func(ctx context.Context, logger *zap.Logger, sm *Manager, envMap EnvironmentConfig, clientset kubernetes.Interface) (pb.FlowCollector, func(*zap.Logger, time.Duration) error, chan struct{})
	// ConnectNetworkFlowsStream establishes the network flows stream
	ConnectNetworkFlowsStream func(ctx context.Context, sm *Manager, logger *zap.Logger) error
	// StartCacheOutReader reads flows from cache and sends to CloudSecure
	StartCacheOutReader func(ctx context.Context, sm *Manager, logger *zap.Logger, keepalivePeriod time.Duration) error
}

// ConnectStreams will continue to reboot and restart the main operations within
// the operator if any disconnects or errors occur.
//
//nolint:gocognit // ConnectStreams is complex due to orchestration of multiple streams
func ConnectStreams(ctx context.Context, logger *zap.Logger, envMap EnvironmentConfig, bufferedGrpcSyncer *logging.BufferedGrpcWriteSyncer, streamFuncs StreamFuncs) {
	falcoEventChan := make(chan string)
	http.HandleFunc("/", collector.NewFalcoEventHandler(falcoEventChan))

	go func() {
		for {
			var listenerConfig net.ListenConfig

			listener, err := listenerConfig.Listen(ctx, "tcp", FalcoPort)
			if err != nil {
				logger.Fatal("Failed to listen on Falco port", zap.String("address", FalcoPort), zap.Error(err))
			}

			falcoEvent := &http.Server{
				Addr:              FalcoPort,
				ReadHeaderTimeout: HTTPReadHeaderTimeout,
				ReadTimeout:       HTTPReadTimeout,
			}

			logger.Info("Falco server listening", zap.String("address", FalcoPort))

			err = falcoEvent.Serve(listener)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("Falco server failed, restarting", zap.Error(err), zap.Duration("delay", ServerRestartDelay))
				time.Sleep(ServerRestartDelay)
			}
		}
	}()

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
			authConContext, authConContextCancel := context.WithCancel(ctx)

			authConn, client, err := NewAuthenticatedConnection(authConContext, logger, envMap)
			if err != nil {
				logger.Error("Failed to establish initial connection; will retry", zap.Error(err))
				resetTimer.Reset(timeutil.JitterTime(ConnectionRetryAfterFailure, ConnectionRetryJitter))
				authConContextCancel()

				failureReason = "Failed to establish initial connection"

				break
			}

			streamClient := &Client{
				Conn:               authConn,
				GrpcClient:         client,
				CiliumNamespaces:   envMap.CiliumNamespaces,
				FalcoEventChan:     falcoEventChan,
				IPFIXCollectorPort: envMap.IPFIXCollectorPort,
			}

			stats := NewStats()

			k8sClient, err := k8sclient.NewClient()
			if err != nil {
				logger.Error("Failed to create Kubernetes client", zap.Error(err))
				authConContextCancel()

				failureReason = "Failed to create Kubernetes client"

				break
			}

			sm := &Manager{
				VerboseDebugging:   envMap.VerboseDebugging,
				Client:             streamClient,
				BufferedGrpcSyncer: bufferedGrpcSyncer,
				FlowCache: NewFlowCache(
					FlowCacheActiveTimeout,
					FlowCacheMaxSize,
					make(chan pb.Flow, FlowChannelBufferSize),
				),
				Stats:     stats,
				K8sClient: k8sClient,
			}

			StartStatsLogger(authConContext, logger, stats, envMap.StatsLogPeriod)

			resourceDone := make(chan struct{})
			logDone := make(chan struct{})
			configDone := make(chan struct{})

			sm.BufferedGrpcSyncer.SetDone(logDone)

			// Start resource stream
			go sm.ManageStream(
				logger.With(zap.String("stream", "SendKubernetesResources")),
				func(l *zap.Logger, d time.Duration) error {
					return streamFuncs.Resources(sm, l, d)
				},
				resourceDone,
				envMap.KeepalivePeriods.KubernetesResources,
				envMap.SuccessPeriods,
			)

			// Start log stream
			go sm.ManageStream(
				logger.With(zap.String("stream", "SendLogs")),
				func(l *zap.Logger, d time.Duration) error {
					return streamFuncs.Logs(sm, l, d)
				},
				logDone,
				envMap.KeepalivePeriods.Logs,
				envMap.SuccessPeriods,
			)

			// Start config stream
			go sm.ManageStream(
				logger.With(zap.String("stream", "GetConfigurationUpdates")),
				func(l *zap.Logger, d time.Duration) error {
					return streamFuncs.Config(sm, l, d)
				},
				configDone,
				envMap.KeepalivePeriods.Configuration,
				envMap.SuccessPeriods,
			)

			// Start flow cache
			flowCacheRunDone := make(chan struct{})
			flowCacheOutReaderDone := make(chan struct{})

			go func() {
				defer close(flowCacheRunDone)

				ctxFlowCacheRun, ctxCancelFlowCacheRun := context.WithCancel(authConContext)
				defer ctxCancelFlowCacheRun()

				err := sm.FlowCache.Run(ctxFlowCacheRun, logger)
				if err != nil {
					logger.Info("Failed to execute flow caching and eviction", zap.Error(err))

					return
				}
			}()

			// Determine and start flow collector
			flowCollector, streamFunc, networkFlowsDone := streamFuncs.DetermineFlowCollector(ctx, logger, sm, envMap, k8sClient.GetClientset())
			sm.Client.FlowCollector = flowCollector

			go sm.ManageStream(
				logger.With(zap.String("stream", "SendKubernetesNetworkFlows")),
				streamFunc,
				networkFlowsDone,
				envMap.KeepalivePeriods.KubernetesNetworkFlows,
				envMap.SuccessPeriods,
			)

			// Start flow cache out reader
			go func() {
				defer close(flowCacheOutReaderDone)

				ctxFlowCacheOutReader, ctxCancelFlowCacheOutReader := context.WithCancel(authConContext)
				defer ctxCancelFlowCacheOutReader()

				err := streamFuncs.ConnectNetworkFlowsStream(ctxFlowCacheOutReader, sm, logger)
				if err != nil {
					logger.Error("Failed to connect to network flows stream", zap.Error(err))

					return
				}

				err = streamFuncs.StartCacheOutReader(ctxFlowCacheOutReader, sm, logger, envMap.KeepalivePeriods.KubernetesNetworkFlows)
				if err != nil {
					logger.Info("Failed to send network flow from cache", zap.Error(err))

					return
				}
			}()

			logger.Info("All streams are open and running")

			select {
			case <-resourceDone:
				failureReason = "Resource stream closed"
			case <-logDone:
				failureReason = "Log stream closed"
			case <-configDone:
				failureReason = "Configuration update stream closed"
			case <-networkFlowsDone:
				failureReason = sm.Client.FlowCollector.String() + " network flow stream closed"
			case <-flowCacheOutReaderDone:
				failureReason = "Flow cache out reader closed"
			case <-flowCacheRunDone:
				failureReason = "Flow cache run closed"
			case <-authConContext.Done():
				failureReason = "Auth context canceled"
			}

			authConContextCancel()
		}

		logger.Warn("One or more streams have been closed; closing and reopening the connection to CloudSecure",
			zap.String("failureReason", failureReason),
			zap.Int("attempt", attempt),
		)
		resetTimer.Reset(timeutil.JitterTime(ConnectionRetryInterval, ConnectionRetryJitter))
	}
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
