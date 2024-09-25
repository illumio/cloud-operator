// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"crypto/rand"
	"math/big"
	"sync"
	"time"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8scluster/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

type streamClient struct {
	conn           *grpc.ClientConn
	client         pb.KubernetesInfoServiceClient
	resourceStream pb.KubernetesInfoService_SendKubernetesResourcesClient
	logStream      pb.KubernetesInfoService_SendLogsClient
}

type deadlockDetector struct {
	processingResources bool
	timeStarted         time.Time
	mutex               sync.RWMutex
}

type streamManager struct {
	instance           *streamClient
	logger             *zap.SugaredLogger
	bufferedGrpcSyncer *BufferedGrpcWriteSyncer
}

type EnvironmentConfig struct {
	// Whether to skip TLS certificate verification when starting a stream.
	TlsSkipVerify bool
	// URL of the onboarding endpoint.
	OnboardingEndpoint string
	// URL of the token endpoint.
	TokenEndpoint string
	// Client ID for onboarding. "" if not specified, i.e. if the operator is not meant to onboard itself.
	OnboardingClientId string
	// Client secret for onboarding. "" if not specified, i.e. if the operator is not meant to onboard itself.
	OnboardingClientSecret string
	// K8s cluster secret name.
	ClusterCreds string
}

var resourceTypes = [2]string{"pods", "nodes"}
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
	for _, resourceType := range resourceTypes {
		allResourcesSnapshotted.Add(1)
		go resourceLister.DyanmicListAndWatchResources(ctx, cancel, resourceType, &allResourcesSnapshotted, &snapshotCompleted)
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
	}
	return nil
}

// Connect and Stream Functions

func connectAndStreamResources(logger *zap.SugaredLogger, sm *streamManager) error {
	resourceCtx, resourceCancel := context.WithCancel(context.Background())
	defer resourceCancel()

	SendKubernetesResourcesStream, err := sm.instance.client.SendKubernetesResources(resourceCtx)
	if err != nil {
		logger.Errorw("Failed to connect to server", "error", err)
		return err
	}

	sm.instance.resourceStream = SendKubernetesResourcesStream

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

	SendLogsStream, err := sm.instance.client.SendLogs(logCtx)
	if err != nil {
		logger.Errorw("Failed to connect to server", "error", err)
		return err
	}

	sm.instance.logStream = SendLogsStream
	sm.bufferedGrpcSyncer.UpdateClient(sm.instance.logStream, sm.instance.conn)

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

		instance := &streamClient{
			conn:   authConn,
			client: client,
		}

		sm := &streamManager{
			instance:           instance,
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
	sm := SecretManager{Logger: logger}

	clientID, clientSecret, err := sm.ReadCredentialsK8sSecrets(ctx, envMap.ClusterCreds)
	if err != nil {
		logger.Errorw("Could not read K8s credentials", "error", err)
	}

	if clientID == "" && clientSecret == "" {
		OnboardingCredentials, err := sm.GetOnboardingCredentials(ctx, envMap.OnboardingClientId, envMap.OnboardingClientSecret)
		if err != nil {
			logger.Errorw("Failed to get onboarding credentials", "error", err)
		}
		am := CredentialsManager{Credentials: OnboardingCredentials, Logger: logger}
		responseData, err := am.Onboard(ctx, envMap.TlsSkipVerify, envMap.OnboardingEndpoint)
		if err != nil {
			logger.Errorw("Failed to register cluster", "error", err)
		}
		err = sm.WriteK8sSecret(ctx, responseData, envMap.ClusterCreds)
		time.Sleep(1 * time.Second)
		if err != nil {
			am.Logger.Errorw("Failed to write secret to Kubernetes", "error", err)
		}
		clientID, clientSecret, err = sm.ReadCredentialsK8sSecrets(ctx, envMap.ClusterCreds)
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
