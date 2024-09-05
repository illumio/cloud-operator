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
}

type streamManager struct {
	instance *streamClient
	logger   *zap.SugaredLogger
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

// NewStream returns a new stream.
func NewStream(ctx context.Context, logger *zap.SugaredLogger, conn *grpc.ClientConn) (*streamManager, error) {
	client := pb.NewKubernetesInfoServiceClient(conn)
	streamKubernetesResources, err := client.SendKubernetesResources(ctx)
	if err != nil {
		// Proper error handling here; you might want to return the error, log it, etc.
		logger.Error(err, "Failed to create a kubernetes resource client")
		return &streamManager{}, err
	}
	streamKubernetesFlows, err := client.SendKubernetesNetworkFlows(ctx)
	if err != nil {
		// Proper error handling here; you might want to return the error, log it, etc.
		logger.Error(err, "Failed to create a kubernetes network flows client")
		return &streamManager{}, err
	}

	// Create or update the instance with the new stream and connection
	instance := &streamClient{
		conn:                      conn,
		streamKubernetesResources: streamKubernetesResources,
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
func (sm *streamManager) BootUpStreamAndReconnect(ctx context.Context) error {
	clusterConfig, err := rest.InClusterConfig()
	if err != nil {
		sm.logger.Error(err, "Error getting in-cluster config")
		return err
	}
	var allResourcesSnapshotted sync.WaitGroup
	var snapshotCompleted sync.WaitGroup
	// Create a dynamic client
	dynamicClient, err := dynamic.NewForConfig(clusterConfig)
	if err != nil {
		sm.logger.Error(err, "Error creating dynamic client")
		return err
	}

	snapshotCompleted.Add(1)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	resourceLister := &ResourceManager{
		logger:        sm.logger,
		dynamicClient: dynamicClient,
		streamManager: sm,
	}
	// TODO: Add logic for a discoveribility function to decide which CNI to use.
	ciliumFlowManager, err := initFlowManager(ctx, sm.logger)
	if err != nil {
		sm.logger.Error(err, "Cannot intialize FlowManager")
	}
	err = resourceLister.sendClusterMetadata(ctx)
	if err != nil {
		sm.logger.Error(err, "Failed to send cluster metadata")
		return err
	}
	for _, resourceType := range resourceTypes {
		allResourcesSnapshotted.Add(1)
		go resourceLister.DyanmicListAndWatchResources(ctx, cancel, resourceType, &allResourcesSnapshotted, &snapshotCompleted)
	}
	allResourcesSnapshotted.Wait()
	err = resourceLister.sendResourceSnapshotComplete()
	if err != nil {
		sm.logger.Error(err, "Failed to send resource snapshot complete")
		return err
	}
	snapshotCompleted.Done()
	err = ciliumFlowManager.listenToFlows(ctx, *sm)
	if err != nil {
		sm.logger.Error(err, "Failed to listen to")
		return err
	}
	<-ctx.Done()
	return err
}

// ExponentialStreamConnect will continue to reboot and restart the main operations within the operator if any disconnects or errors occur.
func ExponentialStreamConnect(ctx context.Context, logger *zap.SugaredLogger, envMap EnvironmentConfig) {
	var backoff = 1 * time.Second
	sm := SecretManager{Logger: logger}
	max := big.NewInt(3)
	for {
		// Generate a random number
		randomInt, err := rand.Int(rand.Reader, max)
		if err != nil {
			logger.Error(err, "Could not generate a random int")
			continue
		}
		result := randomInt.Int64()
		sleep := 1*time.Second + backoff + time.Duration(result)*time.Millisecond // Add randomness
		logger.Info("Failed to establish connection; will retry", "delay", sleep)
		time.Sleep(sleep)
		backoff = backoff * 2 // Exponential increase
		clientID, clientSecret, err := sm.ReadCredentialsK8sSecrets(ctx, envMap.ClusterCreds)
		if err != nil {
			logger.Error(err, "Could not read K8s credentials")
		}
		if clientID == "" && clientSecret == "" {
			OnboardingCredentials, err := sm.GetOnboardingCredentials(ctx, envMap.OnboardingClientId, envMap.OnboardingClientSecret)
			if err != nil {
				logger.Error(err, "Failed to get onboarding credentials")
				continue
			}
			am := CredentialsManager{Credentials: OnboardingCredentials, Logger: logger}
			responseData, err := am.Onboard(ctx, envMap.TlsSkipVerify, envMap.OnboardingEndpoint)
			if err != nil {
				logger.Error(err, "Failed to register cluster")
				continue
			}
			err = sm.WriteK8sSecret(ctx, responseData, envMap.ClusterCreds)
			time.Sleep(1 * time.Second)
			if err != nil {
				am.Logger.Error(err, "Failed to write secret to Kubernetes")
			}
			clientID, clientSecret, err = sm.ReadCredentialsK8sSecrets(ctx, envMap.ClusterCreds)
			if err != nil {
				logger.Error(err, "Could not read K8s credentials")
				continue
			}
		}
		conn, err := SetUpOAuthConnection(ctx, logger, envMap.TokenEndpoint, envMap.TlsSkipVerify, clientID, clientSecret)
		if err != nil {
			logger.Error(err, "Failed to set up an OAuth connection")
			continue
		}
		sm, err := NewStream(ctx, logger, conn)
		if err != nil {
			logger.Error(err, "Failed to create a new stream")
			continue
		}
		err = sm.BootUpStreamAndReconnect(ctx)
		if err != nil {
			logger.Error(err, "Failed to bootup and stream.")
		}
	}
}
