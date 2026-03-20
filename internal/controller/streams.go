// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/auth"
	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/controller/logging"
)

// Generic function to manage any stream with backoff and reconnection logic.
func (sm *streamManager) manageStream(
	logger *zap.Logger,
	connectAndStream func(*zap.Logger, time.Duration) error,
	done chan struct{},
	keepalivePeriod time.Duration,
	streamSuccessPeriod StreamSuccessPeriods,
) {
	defer close(done)

	f := func() error {
		return connectAndStream(logger, keepalivePeriod)
	}

	// If a stream goes down, try to reconnect. With exponential backoff
	funcWithBackoff := func() error {
		return exponentialBackoff(backoffOpts{
			InitialBackoff:       1 * time.Second,
			MaxBackoff:           1 * time.Minute,
			MaxJitterPct:         0.20,
			SevereErrorThreshold: 10,
			ExponentialFactor:    2.0,
			Logger: logger.With(
				zap.String("name", "retry_connect_and_stream"),
			),
			ActionTimeToConsiderSuccess: streamSuccessPeriod.Connect,
		}, f)
	}

	// If repeated attempts to connect fail, that is, the "SevereErrorThreshold"
	// in the above backoff is triggered, then wait and try again. By setting
	// ExponentialFactor to 1, we will wait the same amount of time between every
	// attempt. This is desirable
	funcWithBackoffAndReset := func() error {
		return exponentialBackoff(backoffOpts{
			InitialBackoff:       10 * time.Minute,
			MaxBackoff:           10 * time.Second,
			MaxJitterPct:         0.10,
			SevereErrorThreshold: math.MaxInt,
			// Setting ExponentialFactor 1 will cause the backoff timer to stay
			// constant.
			ExponentialFactor: 1,
			Logger: logger.With(
				zap.String("name", "reset_retry_connect_and_stream"),
			),
			ActionTimeToConsiderSuccess: streamSuccessPeriod.Auth,
		}, funcWithBackoff)
	}

	err := funcWithBackoffAndReset()
	if err != nil {
		logger.Error("Failed to reset connectAndStream. Something is very wrong", zap.Error(err))

		return
	}
}

// ConnectStreams will continue to reboot and restart the main operations within
// the operator if any disconnects or errors occur.
//
//nolint:gocognit // ConnectStreams is complex due to orchestration; refactor pending
func ConnectStreams(ctx context.Context, logger *zap.Logger, envMap EnvironmentConfig, bufferedGrpcSyncer *logging.BufferedGrpcWriteSyncer) {
	// Falco channels communicate news events between http server and our network flows stream,
	falcoEventChan := make(chan string)
	http.HandleFunc("/", collector.NewFalcoEventHandler(falcoEventChan))

	// Start our falco server and have it passively listen, if it fails, try to just restart it.
	go func() {
		for {
			// Create a custom listener, this listener has SO_REUSEADDR option set by default
			var listenerConfig net.ListenConfig

			listener, err := listenerConfig.Listen(ctx, "tcp", falcoPort)
			if err != nil {
				logger.Fatal("Failed to listen on Falco port", zap.String("address", falcoPort), zap.Error(err))
			}

			// Create the HTTP server
			falcoEvent := &http.Server{
				Addr:              falcoPort,
				ReadHeaderTimeout: 5 * time.Second,
				ReadTimeout:       5 * time.Second,
			}

			logger.Info("Falco server listening", zap.String("address", falcoPort))

			err = falcoEvent.Serve(listener)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("Falco server failed, restarting in 5 seconds", zap.Error(err))
				// Giving some time before attempting to restart.....
				time.Sleep(5 * time.Second)
			}
		}
	}()

	// We want to avoid many cloud-operator instances all trying to authenticate
	// at the same time.
	//
	// To that effect, we add a 5 second sleep with 20% jitter here. That way even
	// if you onboard 100 cloud-operators at the same time, they won't all be
	// synced up. Normal network delays may do this for us, but we don't want to
	// rely on that.
	resetTimer := time.NewTimer(jitterTime(5*time.Second, 0.20))
	attempt := 0

	// The happy path blocks inside the for loop.
	// The unhappy path exits the for loop and hits the top-level select.
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
				// When we try this loop again, we wait 10 seconds with 20% jitter.
				resetTimer.Reset(jitterTime(10*time.Second, 0.20))
				authConContextCancel()

				failureReason = "Failed to establish initial connection"

				break
			}

			streamClient := &streamClient{
				conn:               authConn,
				client:             client,
				ciliumNamespaces:   envMap.CiliumNamespaces,
				falcoEventChan:     falcoEventChan,
				ipfixCollectorPort: envMap.IPFIXCollectorPort,
			}

			stats := NewStreamStats()

			// Create the Kubernetes client for dependency injection
			k8sClient, err := NewRealKubernetesClient()
			if err != nil {
				logger.Error("Failed to create Kubernetes client", zap.Error(err))
				authConContextCancel()

				failureReason = "Failed to create Kubernetes client"

				break
			}

			sm := &streamManager{
				verboseDebugging:   envMap.VerboseDebugging,
				streamClient:       streamClient,
				bufferedGrpcSyncer: bufferedGrpcSyncer,
				FlowCache: NewFlowCache(
					20*time.Second, // TODO: Make the active timeout configurable.
					1000,           // TODO: Make the maxFlows capacity configurable.
					make(chan pb.Flow, 100),
				),
				stats:     stats,
				k8sClient: k8sClient,
				clock:     NewRealClock(),
			}

			// Start periodic stats logger
			StartStatsLogger(authConContext, logger, stats, envMap.StatsLogPeriod)

			resourceDone := make(chan struct{})
			logDone := make(chan struct{})
			configDone := make(chan struct{})

			sm.bufferedGrpcSyncer.SetDone(logDone)

			go sm.manageStream(
				logger.With(zap.String("stream", "SendKubernetesResources")),
				sm.connectAndStreamResources,
				resourceDone,
				envMap.KeepalivePeriods.KubernetesResources,
				envMap.StreamSuccessPeriods,
			)

			go sm.manageStream(
				logger.With(zap.String("stream", "SendLogs")),
				sm.connectAndStreamLogs,
				logDone,
				envMap.KeepalivePeriods.Logs,
				envMap.StreamSuccessPeriods,
			)

			go sm.manageStream(
				logger.With(zap.String("stream", "GetConfigurationUpdates")),
				sm.connectAndStreamConfigurationUpdates,
				configDone,
				envMap.KeepalivePeriods.Configuration,
				envMap.StreamSuccessPeriods,
			)

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

			flowCollector, streamFunc, networkFlowsDone := determineFlowCollector(ctx, logger, sm, envMap, k8sClient.GetClientset())
			sm.streamClient.flowCollector = flowCollector

			go sm.manageStream(
				logger.With(zap.String("stream", "SendKubernetesNetworkFlows")),
				streamFunc,
				networkFlowsDone,
				envMap.KeepalivePeriods.KubernetesNetworkFlows,
				envMap.StreamSuccessPeriods,
			)

			go func() {
				defer close(flowCacheOutReaderDone)

				ctxFlowCacheOutReader, ctxCancelFlowCacheOutReader := context.WithCancel(authConContext)
				defer ctxCancelFlowCacheOutReader()

				err := sm.connectNetworkFlowsStream(ctxFlowCacheOutReader, logger)
				if err != nil {
					logger.Error("Failed to connect to network flows stream", zap.Error(err))

					return
				}

				err = sm.startFlowCacheOutReader(ctxFlowCacheOutReader, logger, envMap.KeepalivePeriods.KubernetesNetworkFlows)
				if err != nil {
					logger.Info("Failed to send network flow from cache", zap.Error(err))

					return
				}
			}()

			// Block until one of the streams fail. Then we will jump to the top of
			// this loop & try again: authenticate and open the streams.
			logger.Info("All streams are open and running")

			select {
			case <-resourceDone:
				failureReason = "Resource stream closed"
			case <-logDone:
				failureReason = "Log stream closed"
			case <-configDone:
				failureReason = "Configuration update stream closed"
			case <-networkFlowsDone:
				failureReason = sm.streamClient.flowCollector.String() + " network flow stream closed"
			case <-flowCacheOutReaderDone:
				failureReason = "Flow cache reader failed"
			case <-flowCacheRunDone:
				failureReason = "Flow cache running process failed"
			}

			authConContextCancel()
		}

		logger.Warn("One or more streams have been closed; closing and reopening the connection to CloudSecure",
			zap.String("failureReason", failureReason),
			zap.Int("attempt", attempt),
		)
		resetTimer.Reset(jitterTime(5*time.Second, 0.20))
	}
}

// NewAuthenticatedConnection gets a valid token and creats a connection to CloudSecure.
func NewAuthenticatedConnection(ctx context.Context, logger *zap.Logger, envMap EnvironmentConfig) (*grpc.ClientConn, pb.KubernetesInfoServiceClient, error) {
	clientset, err := NewClientSet()
	if err != nil {
		return nil, nil, err
	}

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

// jitterTime subtracts a percentage from the base time, in order to introduce
// jitter. maxJitterPct must be in the range [0, 1).
//
// jitter is a technical term, meaning "a signal's deviation from true
// periodicity". This is desirable in distributed systems, because if all agents
// synchronize their messages, we stop calling that API requests and start
// calling that a DDoS attack.
func jitterTime(base time.Duration, maxJitterPct float64) time.Duration {
	// Jitter percentage is in range [0, maxJitterPct)
	jitterPct := rand.Float64() * maxJitterPct //nolint:gosec

	return time.Duration(float64(base) * (1. - jitterPct))
}
