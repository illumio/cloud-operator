// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"sync"
	"time"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

type streamClient struct {
	ciliumNamespace          string
	ciliumNetworkFlowsStream pb.KubernetesInfoService_SendKubernetesNetworkFlowsClient
	conn                     *grpc.ClientConn
	client                   pb.KubernetesInfoServiceClient
	logStream                pb.KubernetesInfoService_SendLogsClient
	resourceStream           pb.KubernetesInfoService_SendKubernetesResourcesClient
}

type deadlockDetector struct {
	mutex               sync.RWMutex
	processingResources bool
	timeStarted         time.Time
}

type streamManager struct {
	bufferedGrpcSyncer *BufferedGrpcWriteSyncer
	logger             *zap.SugaredLogger
	streamClient       *streamClient
}

type EnvironmentConfig struct {
	// Namspace of Cilium.
	CiliumNamespace string
	// K8s cluster secret name.
	ClusterCreds string
	// Client ID for onboarding. "" if not specified, i.e. if the operator is not meant to onboard itself.
	OnboardingClientId string
	// Client secret for onboarding. "" if not specified, i.e. if the operator is not meant to onboard itself.
	OnboardingClientSecret string
	// URL of the onboarding endpoint.
	OnboardingEndpoint string
	// URL of the token endpoint.
	TokenEndpoint string
	// Whether to skip TLS certificate verification when starting a stream.
	TlsSkipVerify bool
}

var resourceAPIGroupMap = map[string]string{
	"cronjobs":                  "batch",
	"customresourcedefinitions": "apiextensions.k8s.io",
	"daemonsets":                "apps",
	"deployments":               "apps",
	"endpoints":                 "",
	"gateways":                  "gateway.networking.k8s.io",
	"gatewayclasses":            "gateway.networking.k8s.io",
	"httproutes":                "gateway.networking.k8s.io",
	"ingresses":                 "networking.k8s.io",
	"ingressclasses":            "networking.k8s.io",
	"jobs":                      "batch",
	"networkpolicies":           "networking.k8s.io",
	"nodes":                     "",
	"pods":                      "",
	"replicasets":               "apps",
	"replicationcontrollers":    "",
	"serviceaccounts":           "",
	"services":                  "",
	"statefulsets":              "apps",
}

var dd = &deadlockDetector{}

// ServerIsHealthy checks if a deadlock has occured within the threaded resource listing process.
func ServerIsHealthy() bool {
	dd.mutex.RLock()
	defer dd.mutex.RUnlock()
	if dd.processingResources && time.Since(dd.timeStarted) > 5*time.Minute {
		return false
	}
	return true
}

// StreamResources handles the resource stream.
func (sm *streamManager) StreamResources(ctx context.Context, cancel context.CancelFunc) error {
	defer func() {
		dd.processingResources = false
	}()
	clusterConfig, err := rest.InClusterConfig()
	if err != nil {
		sm.logger.Errorw("Error getting in-cluster config", "error", err)
		return err
	}
	var allResourcesSnapshotted sync.WaitGroup
	var snapshotCompleted sync.WaitGroup
	// Create a dynamic client
	dynamicClient, err := dynamic.NewForConfig(clusterConfig)
	if err != nil {
		sm.logger.Errorw("Error creating dynamic client", "error", err)
		return err
	}

	clientset, err := NewClientSet()
	if err != nil {
		sm.logger.Errorw("Failed to create clientset", "error", err)
		return err
	}
	apiGroups, err := clientset.Discovery().ServerGroups()
	if err != nil {
		sm.logger.Error("Failed to discover API groups", "error", err)
	}
	foundGatewayAPIGroup := false

	// Check if the "gateway.networking.k8s.io" API group is not available, if it is not delete those resources and groups
	for _, group := range apiGroups.Groups {
		if group.Name == "gateway.networking.k8s.io" {
			foundGatewayAPIGroup = true
			break
		}
	}

	// If the "gateway.networking.k8s.io" API group is not found, remove the resources
	if !foundGatewayAPIGroup {
		gatewayResources := []string{"gateways", "gatewayclasses", "httproutes"}
		for _, resource := range gatewayResources {
			delete(resourceAPIGroupMap, resource)
		}
	}

	snapshotCompleted.Add(1)
	dd.mutex.Lock()
	dd.timeStarted = time.Now()
	dd.processingResources = true
	dd.mutex.Unlock()
	resourceLister := &ResourceManager{
		logger:        sm.logger,
		dynamicClient: dynamicClient,
		streamManager: sm,
	}
	err = resourceLister.sendClusterMetadata(ctx)
	if err != nil {
		sm.logger.Errorw("Failed to send cluster metadata", "error", err)
		return err
	}
	for resource, apiGroup := range resourceAPIGroupMap {
		allResourcesSnapshotted.Add(1)
		go resourceLister.DyanmicListAndWatchResources(ctx, cancel, resource, apiGroup, &allResourcesSnapshotted, &snapshotCompleted)
	}
	allResourcesSnapshotted.Wait()
	err = resourceLister.sendResourceSnapshotComplete()
	dd.timeStarted = time.Now()
	dd.mutex.Lock()
	dd.processingResources = false
	dd.mutex.Unlock()
	if err != nil {
		sm.logger.Errorw("Failed to send resource snapshot complete", "error", err)
		return err
	}
	snapshotCompleted.Done()

	<-ctx.Done()
	return ctx.Err()
}

// StreamLogs handles the log stream.
func (sm *streamManager) StreamLogs(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		errCh <- sm.bufferedGrpcSyncer.ListenToLogStream()
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		if err != nil {
			return err
		}
	}
	return nil
}

// StreamLogs handles the log stream.
func (sm *streamManager) StreamCiliumNetworkFlows(ctx context.Context, ciliumNamespace string) error {
	// TODO: Add logic for a discoveribility function to decide which CNI to use.
	ciliumFlowManager, err := newCiliumCollector(ctx, sm.logger, ciliumNamespace)
	if err != nil {
		sm.logger.Infow("Failed to initialize Cilium Hubble Relay flow collector; disabling flow collector", "error", err)
		return err
	}
	if ciliumFlowManager != nil {
		for {
			err = ciliumFlowManager.exportCiliumFlows(ctx, *sm)
			if err != nil {
				sm.logger.Warnw("Failed to listen to flows", "error", err)
				return err
			}
		}
	}
	return nil
}

// connectAndStreamCiliumNetworkFlows creates ciliumNetworkFlows client and begins the streaming of network flows.
func connectAndStreamCiliumNetworkFlows(logger *zap.SugaredLogger, sm *streamManager) error {
	ciliumCtx, ciliumCancel := context.WithCancel(context.Background())
	defer ciliumCancel()

	sendCiliumNetworkFlowsStream, err := sm.streamClient.client.SendKubernetesNetworkFlows(ciliumCtx)
	if err != nil {
		logger.Errorw("Failed to connect to server", "error", err)
		return err
	}

	sm.streamClient.ciliumNetworkFlowsStream = sendCiliumNetworkFlowsStream

	err = sm.StreamCiliumNetworkFlows(ciliumCtx, sm.streamClient.ciliumNamespace)
	if err != nil {
		logger.Errorw("Failed to bootup and stream Cilium network flows", "error", err)
		return err
	}

	return nil
}

// connectAndStreamResources creates resourceStream client and begins the streaming of resources.
func connectAndStreamResources(logger *zap.SugaredLogger, sm *streamManager) error {
	resourceCtx, resourceCancel := context.WithCancel(context.Background())
	defer resourceCancel()

	SendKubernetesResourcesStream, err := sm.streamClient.client.SendKubernetesResources(resourceCtx)
	if err != nil {
		logger.Errorw("Failed to connect to server", "error", err)
		return err
	}

	sm.streamClient.resourceStream = SendKubernetesResourcesStream

	err = sm.StreamResources(resourceCtx, resourceCancel)
	if err != nil {
		logger.Errorw("Failed to bootup and stream resources", "error", err)
		return err
	}

	return nil
}

// connectAndStreamLogs creates sendLogs client and begins the streaming of logs.
func connectAndStreamLogs(logger *zap.SugaredLogger, sm *streamManager) error {
	logCtx, logCancel := context.WithCancel(context.Background())
	defer logCancel()

	SendLogsStream, err := sm.streamClient.client.SendLogs(logCtx)
	if err != nil {
		logger.Errorw("Failed to connect to server", "error", err)
		return err
	}

	sm.streamClient.logStream = SendLogsStream
	sm.bufferedGrpcSyncer.UpdateClient(sm.streamClient.logStream, sm.streamClient.conn)

	err = sm.StreamLogs(logCtx)
	if err != nil {
		logger.Errorw("Failed to bootup and stream logs", "error", err)
		return err
	}

	return nil
}

// Generic function to manage any stream with backoff and reconnection logic.
func manageStream(ctx context.Context, logger *zap.SugaredLogger, connectAndStream func(*zap.SugaredLogger, *streamManager) error, sm *streamManager, done chan struct{}) error {
	const (
		initialBackoff       = 1 * time.Second
		maxBackoff           = 1 * time.Minute
		maxJitterPct         = 0.20
		resetPeriod          = 10 * time.Minute
		severeErrorThreshold = 10 // Define what constitutes a severe error.
	)

	var (
		backoff             = initialBackoff
		consecutiveFailures = 0
	)
	max := big.NewInt(3)

	resetTimer := time.NewTimer(resetPeriod)
	for {
		select {
		case <-ctx.Done():
			close(done)
			return nil

		case <-resetTimer.C:
			consecutiveFailures = 0
			backoff = initialBackoff
			resetTimer.Reset(resetPeriod)

		default:
			err := connectAndStream(logger, sm)
			if err != nil {
				logger.Errorw("Failed to establish stream connection; will retry", "error", err)
				consecutiveFailures++

				if consecutiveFailures >= severeErrorThreshold {
					close(done)
					return errors.New("severe failure in manageStream: exceeded severe error threshold")
				}

				randomInt, err := rand.Int(rand.Reader, max)
				if err != nil {
					logger.Errorw("Could not generate a random int", "error", err)
					continue
				}

				jitterPct, _ := randomInt.Float64()
				jitterPct = jitterPct * maxJitterPct
				sleep := time.Duration(float64(backoff) * (1. - jitterPct))

				if sleep < initialBackoff {
					sleep = initialBackoff
				}
				if sleep > maxBackoff {
					sleep = maxBackoff
				}

				logger.Infow("Sleeping before retrying connection", "backoff", sleep)
				time.Sleep(sleep)
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			} else {
				consecutiveFailures = 0
				backoff = initialBackoff
			}
		}
	}
}

// ExponentialStreamConnect will continue to reboot and restart the main operations within the operator if any disconnects or errors occur.
func ExponentialStreamConnect(ctx context.Context, logger *zap.SugaredLogger, envMap EnvironmentConfig, bufferedGrpcSyncer *BufferedGrpcWriteSyncer) {
	for {
		authConn, client, err := NewAuthenticatedCon(ctx, logger, envMap)
		if err != nil {
			logger.Errorw("Failed to establish initial connection; will retry", "error", err)
			time.Sleep(5 * time.Second) // Retry after a delay
			continue
		}

		streamClient := &streamClient{
			conn:            authConn,
			client:          client,
			ciliumNamespace: envMap.CiliumNamespace,
		}

		sm := &streamManager{
			streamClient:       streamClient,
			logger:             logger,
			bufferedGrpcSyncer: bufferedGrpcSyncer,
		}

		resourceDone := make(chan struct{})
		logDone := make(chan struct{})
		ciliumDone := make(chan struct{})
		sm.bufferedGrpcSyncer.done = logDone

		go manageStream(ctx, logger, connectAndStreamResources, sm, resourceDone)
		go manageStream(ctx, logger, connectAndStreamLogs, sm, logDone)
		go manageStream(ctx, logger, connectAndStreamCiliumNetworkFlows, sm, ciliumDone)

		select {
		case <-ciliumDone:
		case <-resourceDone:
		case <-logDone:
		}

		logger.Warn("All streams have been closed. Rebooting the entire connection.")
	}
}

// streamAuth handles getting a valid token and creating a connection
func NewAuthenticatedCon(ctx context.Context, logger *zap.SugaredLogger, envMap EnvironmentConfig) (*grpc.ClientConn, pb.KubernetesInfoServiceClient, error) {
	authn := Authenticator{Logger: logger}

	clientID, clientSecret, err := authn.ReadCredentialsK8sSecrets(ctx, envMap.ClusterCreds)
	if err != nil {
		logger.Errorw("Could not read K8s credentials", "error", err)
	}

	if clientID == "" && clientSecret == "" {
		OnboardingCredentials, err := authn.GetOnboardingCredentials(ctx, envMap.OnboardingClientId, envMap.OnboardingClientSecret)
		if err != nil {
			logger.Errorw("Failed to get onboarding credentials", "error", err)
		}
		responseData, err := Onboard(ctx, envMap.TlsSkipVerify, envMap.OnboardingEndpoint, OnboardingCredentials, logger)
		if err != nil {
			logger.Errorw("Failed to register cluster", "error", err)
		}
		err = authn.WriteK8sSecret(ctx, responseData, envMap.ClusterCreds)
		time.Sleep(1 * time.Second)
		if err != nil {
			logger.Errorw("Failed to write secret to Kubernetes", "error", err)
		}
		clientID, clientSecret, err = authn.ReadCredentialsK8sSecrets(ctx, envMap.ClusterCreds)
		if err != nil {
			logger.Errorw("Could not read K8s credentials", "error", err)
		}
	}

	conn, err := SetUpOAuthConnection(ctx, logger, envMap.TokenEndpoint, envMap.TlsSkipVerify, clientID, clientSecret)
	if err != nil {
		logger.Errorw("Failed to set up an OAuth connection", "error", err)
		return nil, nil, err
	}

	client := pb.NewKubernetesInfoServiceClient(conn)

	return conn, client, err
}
