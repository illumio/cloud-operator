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
	conn                      *grpc.ClientConn
	streamKubernetesResources pb.KubernetesInfoService_SendKubernetesResourcesClient
	streamKubernetesFlows     pb.KubernetesInfoService_SendKubernetesNetworkFlowsClient
	logStream                 pb.KubernetesInfoService_SendLogsClient
}

type deadlockDetector struct {
	processingResources bool
	timeStarted         time.Time
	mutex               sync.RWMutex
}

type streamManager struct {
	instance *streamClient
	logger   *zap.SugaredLogger
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

// NewStream returns a new stream.
func NewStreams(ctx context.Context, logger *zap.SugaredLogger, conn *grpc.ClientConn) (*streamManager, error) {
	client := pb.NewKubernetesInfoServiceClient(conn)

	SendLogsStream, err := client.SendLogs(ctx)
	if err != nil {
		// Proper error handling here; you might want to return the error, log it, etc.
		logger.Errorw("Failed to connect to server",
			"error", err,
		)
		return &streamManager{}, err
	}
	SendKubernetesResourcesStream, err := client.SendKubernetesResources(ctx)
	if err != nil {
		// Proper error handling here; you might want to return the error, log it, etc.
		logger.Errorw("Failed to connect to server", "error", err)
		return &streamManager{}, err
	}
	streamKubernetesFlows, err := client.SendKubernetesNetworkFlows(ctx)
	if err != nil {
		logger.Errorw("Failed to create a kubernetes network flows client", err)
		return &streamManager{}, err
	}

	instance := &streamClient{
		conn:                      conn,
		streamKubernetesResources: SendKubernetesResourcesStream,
		logStream:                 SendLogsStream,
		streamKubernetesFlows:     streamKubernetesFlows,
	}
	sm := &streamManager{
		instance: instance,
		logger:   logger,
	}
	return sm, nil
}

// BootUpStreamAndReconnect creates the clients needed to read K8s resources and
// also creates all of the objects that help organize and pass info to the goroutines that are listing and watching
// different resource types. This is also handling the asyc nature of listing resources and properly sending our commit
// message on intial boot when we have the state of the cluster.
func (sm *streamManager) BootUpStreamAndReconnect(ctx context.Context, cancel context.CancelFunc, ciliumNamespace string) error {
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
	// TODO: Add logic for a discoveribility function to decide which CNI to use.
	ciliumFlowManager, err := newCollector(ctx, sm.logger, ciliumNamespace)
	if err != nil {
		sm.logger.Infow("Failed to initialize Cilium Hubble Relay flow collector; disabling flow collector", "error", err)
	}
	if ciliumFlowManager.client != nil {
		go func() {
			for {
				err = ciliumFlowManager.readFlows(ctx, *sm)
				if err != nil {
					sm.logger.Warnw("Failed to listen to flows", "error", err)
				}
			}
		}()
	}
	return nil
}

// ExponentialStreamConnect will continue to reboot and restart the main operations within the operator if any disconnects or errors occur.
func ExponentialStreamConnect(ctx context.Context, logger *zap.SugaredLogger, envMap EnvironmentConfig, bufferedGrpcSyncer *BufferedGrpcWriteSyncer) {
	var backoff = 1 * time.Second
	sm := SecretManager{Logger: logger}
	max := big.NewInt(3)
	for {
		// Generate a random number
		randomInt, err := rand.Int(rand.Reader, max)
		if err != nil {
			logger.Errorw("Could not generate a random int", "error", err)
			continue
		}
		result := randomInt.Int64()
		sleep := 1*time.Second + backoff + time.Duration(result)*time.Millisecond // Add randomness
		logger.Infow("Failed to establish connection; will retry", "delay", sleep)
		time.Sleep(sleep)
		backoff = backoff * 2 // Exponential increase
		clientID, clientSecret, err := sm.ReadCredentialsK8sSecrets(ctx, envMap.ClusterCreds)
		if err != nil {
			logger.Errorw("Could not read K8s credentials", "error", err)
		}
		if clientID == "" && clientSecret == "" {
			OnboardingCredentials, err := sm.GetOnboardingCredentials(ctx, envMap.OnboardingClientId, envMap.OnboardingClientSecret)
			if err != nil {
				logger.Errorw("Failed to get onboarding credentials", "error", err)
				continue
			}
			am := CredentialsManager{Credentials: OnboardingCredentials, Logger: logger}
			responseData, err := am.Onboard(ctx, envMap.TlsSkipVerify, envMap.OnboardingEndpoint)
			if err != nil {
				logger.Errorw("Failed to register cluster", "error", err)
				continue
			}
			err = sm.WriteK8sSecret(ctx, responseData, envMap.ClusterCreds)
			time.Sleep(1 * time.Second)
			if err != nil {
				am.Logger.Errorw("Failed to write secret to Kubernetes", "error", err)
			}
			clientID, clientSecret, err = sm.ReadCredentialsK8sSecrets(ctx, envMap.ClusterCreds)
			if err != nil {
				logger.Errorw("Could not read K8s credentials", "error", err)
				continue
			}
		}
		conn, err := SetUpOAuthConnection(ctx, logger, envMap.TokenEndpoint, envMap.TlsSkipVerify, clientID, clientSecret)
		if err != nil {
			logger.Errorw("Failed to set up an OAuth connection", "error", err)
			continue
		}
		sm, err := NewStreams(ctx, logger, conn)
		if err != nil {
			logger.Errorw("Failed to create a new stream", "error", err)
			continue
		}

		// Update the gRPC client and connection in BufferedGrpcWriteSyncer
		bufferedGrpcSyncer.UpdateClient(sm.instance.logStream, sm.instance.conn)
		go bufferedGrpcSyncer.ListenToLogStream()

		ctx, cancel := context.WithCancel(ctx)
		err = sm.BootUpStreamAndReconnect(ctx, cancel, envMap.CiliumNamespace)
		if err != nil {
			cancel()
			logger.Errorw("Failed to bootup and stream", "error", err)
			continue
		}
		<-ctx.Done()
	}
}
