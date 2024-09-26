// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"crypto/rand"
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
	conn                      *grpc.ClientConn
	client                    pb.KubernetesInfoServiceClient
	streamKubernetesResources pb.KubernetesInfoService_SendKubernetesResourcesClient
	ciliumNetworkFlowsStream  pb.KubernetesInfoService_SendKubernetesNetworkFlowsClient
	logStream                 pb.KubernetesInfoService_SendLogsClient
}

type deadlockDetector struct {
	processingResources bool
	timeStarted         time.Time
	mutex               sync.RWMutex
}

type streamManager struct {
	bufferedGrpcSyncer *BufferedGrpcWriteSyncer
	streamClient       *streamClient
	logger             *zap.SugaredLogger
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
func (sm *streamManager) StreamLogs(ctx context.Context, cancel context.CancelFunc) error {
	errCh := make(chan error, 1)

	go func() {
		errCh <- sm.bufferedGrpcSyncer.ListenToLogStream()
	}()

	select {
	case <-ctx.Done():
		cancel()
		return ctx.Err()
	case err := <-errCh:
		if err != nil {
			cancel()
			return err
		}
		// TODO: Add logic for a discoveribility function to decide which CNI to use.
		// 	ciliumFlowManager, err := newCiliumCollector(ctx, sm.logger, ciliumNamespace)
		// 	if err != nil {
		// 		sm.logger.Infow("Failed to initialize Cilium Hubble Relay flow collector; disabling flow collector", "error", err)
		// 	}
		// 	if ciliumFlowManager.client != nil {
		// 		go func() {
		// 			for {
		// 				err = ciliumFlowManager.exportCiliumFlows(ctx, *sm)
		// 				if err != nil {
		// 					sm.logger.Warnw("Failed to listen to flows", "error", err)
		// 					// Attempt to rediscover new hubble address and reconnect
		// 					for {
		// 						ciliumFlowManager, err = newCiliumCollector(ctx, sm.logger, ciliumNamespace)
		// 						if err != nil {
		// 							sm.logger.Warnw("Failed to recreate new Collector", "error", err)
		// 						} else {
		// 							break
		// 						}
		// 						// TODO: Add exponetial backoff so that this isnt spammed as hubble relay is restarted/deployed
		// 						// TODO: redo looping logic to be cleaner
		// 					}
		// 				}
		// 			}
		// 		}()
	}
	return nil
}

// Connect and Stream Functions

func connectAndStreamResources(logger *zap.SugaredLogger, sm *streamManager) error {
	resourceCtx, resourceCancel := context.WithCancel(context.Background())
	defer resourceCancel()

	SendKubernetesResourcesStream, err := sm.streamClient.client.SendKubernetesResources(resourceCtx)
	if err != nil {
		logger.Errorw("Failed to connect to server", "error", err)
		return err
	}

	sm.streamClient.streamKubernetesResources = SendKubernetesResourcesStream

	err = sm.StreamResources(resourceCtx, resourceCancel)
	if err != nil {
		logger.Errorw("Failed to bootup and stream resources", "error", err)
		return err
	}

	return nil
}

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

	err = sm.StreamLogs(logCtx, logCancel)
	if err != nil {
		logger.Errorw("Failed to bootup and stream logs", "error", err)
		return err
	}

	return nil
}

// Generic function to manage any stream with backoff and reconnection logic.
func manageStream(logger *zap.SugaredLogger, connectAndStream func(*zap.SugaredLogger, *streamManager) error, sm *streamManager, done chan struct{}) {
	var backoff = 1 * time.Second
	max := big.NewInt(3)

	for {
		err := connectAndStream(logger, sm)
		if err != nil {
			logger.Errorw("Failed to establish stream connection; will retry", "error", err)
			randomInt, err := rand.Int(rand.Reader, max)
			if err != nil {
				logger.Errorw("Could not generate a random int", "error", err)
				continue
			}
			result := randomInt.Int64()
			sleep := 1*time.Second + backoff + time.Duration(result)*time.Millisecond // Add randomness
			time.Sleep(sleep)
			backoff = backoff * 2 // Exponential increase
		} else {
			done <- struct{}{}
			return
		}
	}
}

// ExponentialStreamConnect will continue to reboot and restart the main operations within the operator if any disconnects or errors occur.
func ExponentialStreamConnect(ctx context.Context, logger *zap.SugaredLogger, envMap EnvironmentConfig, bufferedGrpcSyncer *BufferedGrpcWriteSyncer) {
	for {
		authConn, client, err := streamAuth(ctx, logger, envMap)
		if err != nil {
			logger.Errorw("Failed to establish initial connection; will retry", "error", err)
			time.Sleep(5 * time.Second) // Retry after a delay
			continue
		}

		streamClient := &streamClient{
			conn:   authConn,
			client: client,
		}

		sm := &streamManager{
			streamClient:       streamClient,
			logger:             logger,
			bufferedGrpcSyncer: bufferedGrpcSyncer,
		}

		resourceDone := make(chan struct{})
		logDone := make(chan struct{})
		sm.bufferedGrpcSyncer.done = logDone

		go manageStream(logger, connectAndStreamResources, sm, resourceDone)
		go manageStream(logger, connectAndStreamLogs, sm, logDone)

		select {
		case <-resourceDone:
		case <-logDone:
		}

		logger.Warn("All streams have been closed. Rebooting the entire connection.")
	}
}

func streamAuth(ctx context.Context, logger *zap.SugaredLogger, envMap EnvironmentConfig) (*grpc.ClientConn, pb.KubernetesInfoServiceClient, error) {
	authn := Authenticator{Logger: logger}

	clientID, clientSecret, err := authn.ReadCredentialsK8sSecrets(ctx, envMap.ClusterCreds)
	if err != nil {
		logger.Errorw("Could not read K8s credentials", "error", err)
	}

	if clientID == "" && clientSecret == "" {
		OnboardingCredentials, err := authn.GetOnboardingCredentials(ctx, envMap.OnboardingClientId, envMap.OnboardingClientSecret)
		ctx, _ := context.WithCancel(ctx)
		// 		err = sm.BootUpStreamAndReconnect(ctx, cancel, envMap.CiliumNamespace)
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
