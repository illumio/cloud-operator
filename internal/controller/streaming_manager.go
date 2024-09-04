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
	conn   *grpc.ClientConn
	stream pb.KubernetesInfoService_SendKubernetesResourcesClient
}

type streamManager struct {
	instance *streamClient
	logger   *zap.SugaredLogger
}

// TODO: Create a struct that holds all of the env variables to more easily pass them in with static types

var resourceTypes = [2]string{"pods", "nodes"}

// NewStream returns a new stream.
func NewStream(ctx context.Context, logger *zap.SugaredLogger, conn *grpc.ClientConn) (*streamManager, error) {
	client := pb.NewKubernetesInfoServiceClient(conn)
	stream, err := client.SendKubernetesResources(ctx)
	if err != nil {
		// Proper error handling here; you might want to return the error, log it, etc.
		logger.Error("Failed to connect to server", "error", err)
		return &streamManager{}, err
	}

	// Create or update the instance with the new stream and connection
	instance := &streamClient{
		conn:   conn,
		stream: stream,
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
		sm.logger.Error("Error getting in-cluster config", "error", err)
		return err
	}
	var allResourcesSnapshotted sync.WaitGroup
	var snapshotCompleted sync.WaitGroup
	// Create a dynamic client
	dynamicClient, err := dynamic.NewForConfig(clusterConfig)
	if err != nil {
		sm.logger.Error("Error creating dynamic client", "error", err)
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
	err = resourceLister.sendClusterMetadata(ctx)
	if err != nil {
		sm.logger.Error("Failed to send cluster metadata", "error", err)
		return err
	}
	for _, resourceType := range resourceTypes {
		allResourcesSnapshotted.Add(1)
		go resourceLister.DyanmicListAndWatchResources(ctx, cancel, resourceType, &allResourcesSnapshotted, &snapshotCompleted)
	}
	allResourcesSnapshotted.Wait()
	err = resourceLister.sendResourceSnapshotComplete()
	if err != nil {
		sm.logger.Error("Failed to send resource snapshot complete", "error", err)
		return err
	}
	snapshotCompleted.Done()
	<-ctx.Done()
	return err
}

// ExponentialStreamConnect will continue to reboot and restart the main operations within the operator if any disconnects or errors occur.
func ExponentialStreamConnect(ctx context.Context, logger *zap.SugaredLogger, envMap map[string]interface{}) {
	var backoff = 1 * time.Second
	sm := SecretManager{Logger: logger}
	max := big.NewInt(3)
	for {
		// Generate a random number
		randomInt, err := rand.Int(rand.Reader, max)
		if err != nil {
			logger.Error("Could not generate a random int", "error", err)
			continue
		}
		result := randomInt.Int64()
		sleep := 1*time.Second + backoff + time.Duration(result)*time.Millisecond // Add randomness
		logger.Info("Failed to establish connection; will retry", "delay", sleep)
		time.Sleep(sleep)
		backoff = backoff * 2 // Exponential increase
		clientID, clientSecret, err := sm.ReadCredentialsK8sSecrets(ctx, envMap["ClusterCreds"].(string))
		if err != nil {
			logger.Error("Could not read K8s credentials", "error", err)
		}
		if clientID == "" && clientSecret == "" {
			OnboardingCredentials, err := sm.GetOnboardingCredentials(ctx, envMap["OnboardingClientId"].(string), envMap["OnboardingClientSecret"].(string))
			if err != nil {
				logger.Error("Failed to get onboarding credentials", "error", err)
				continue
			}
			am := CredentialsManager{Credentials: OnboardingCredentials, Logger: logger}
			responseData, err := am.Onboard(ctx, envMap["TlsSkipVerify"].(bool), envMap["OnboardingEndpoint"].(string))
			if err != nil {
				logger.Error("Failed to register cluster", "error", err)
				continue
			}
			err = sm.WriteK8sSecret(ctx, responseData, envMap["ClusterCreds"].(string))
			time.Sleep(1 * time.Second)
			if err != nil {
				am.Logger.Error("Failed to write secret to Kubernetes", "error", err)
			}
			clientID, clientSecret, err = sm.ReadCredentialsK8sSecrets(ctx, envMap["ClusterCreds"].(string))
			if err != nil {
				logger.Error("Could not read K8s credentials", "error", err)
				continue
			}
		}
		conn, err := SetUpOAuthConnection(ctx, logger, envMap["TokenEndpoint"].(string), envMap["TlsSkipVerify"].(bool), clientID, clientSecret)
		if err != nil {
			logger.Error("Failed to set up an OAuth connection", "error", err)
			continue
		}
		sm, err := NewStream(ctx, logger, conn)
		if err != nil {
			logger.Error("Failed to create a new stream", "error", err)
			continue
		}
		err = sm.BootUpStreamAndReconnect(ctx)
		if err != nil {
			logger.Error("Failed to bootup and stream.", "error", err)
		}
	}
}
