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
	resourceStream pb.KubernetesInfoService_SendKubernetesResourcesClient
	logStream      pb.KubernetesInfoService_KubernetesLogsClient
}

type streamManager struct {
	instance *streamClient
	logger   *zap.SugaredLogger
}

// TODO: Create a struct that holds all of the env variables to more easily pass them in with static types

var resourceTypes = [2]string{"pods", "nodes"}

// NewStream returns a new stream.
func NewStreams(ctx context.Context, logger *zap.SugaredLogger, conn *grpc.ClientConn) (*streamManager, error) {
	client := pb.NewKubernetesInfoServiceClient(conn)

	KubernetesLogsStream, err := client.KubernetesLogs(ctx)
	if err != nil {
		// Proper error handling here; you might want to return the error, log it, etc.
		logger.Error(err, "Failed to connect to server")
		return &streamManager{}, err
	}
	SendKubernetesResourcesStream, err := client.SendKubernetesResources(ctx)
	if err != nil {
		// Proper error handling here; you might want to return the error, log it, etc.
		logger.Error(err, "Failed to connect to server")
		return &streamManager{}, err
	}

	instance := &streamClient{
		conn:           conn,
		resourceStream: SendKubernetesResourcesStream,
		logStream:      KubernetesLogsStream,
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
	<-ctx.Done()
	return err
}

// ExponentialStreamConnect will continue to reboot and restart the main operations within the operator if any disconnects or errors occur.
func ExponentialStreamConnect(ctx context.Context, logger *zap.SugaredLogger, envMap map[string]interface{}, bufferedGrpcSyncer *BufferedGrpcWriteSyncer) {
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
		clientID, clientSecret, err := sm.ReadCredentialsK8sSecrets(ctx, envMap["ClusterCreds"].(string))
		if err != nil {
			logger.Error(err, "Could not read K8s credentials")
		}
		if clientID == "" && clientSecret == "" {
			OnboardingCredentials, err := sm.GetOnboardingCredentials(ctx, envMap["OnboardingClientId"].(string), envMap["OnboardingClientSecret"].(string))
			if err != nil {
				logger.Error(err, "Failed to get onboarding credentials")
				continue
			}
			am := CredentialsManager{Credentials: OnboardingCredentials, Logger: logger}
			responseData, err := am.Onboard(ctx, envMap["TlsSkipVerify"].(bool), envMap["OnboardingEndpoint"].(string))
			if err != nil {
				logger.Error(err, "Failed to register cluster")
				continue
			}
			err = sm.WriteK8sSecret(ctx, responseData, envMap["ClusterCreds"].(string))
			time.Sleep(1 * time.Second)
			if err != nil {
				am.Logger.Error(err, "Failed to write secret to Kubernetes")
			}
			clientID, clientSecret, err = sm.ReadCredentialsK8sSecrets(ctx, envMap["ClusterCreds"].(string))
			if err != nil {
				logger.Error(err, "Could not read K8s credentials")
				continue
			}
		}
		conn, err := SetUpOAuthConnection(ctx, logger, envMap["TokenEndpoint"].(string), envMap["TlsSkipVerify"].(bool), clientID, clientSecret)
		if err != nil {
			logger.Error(err, "Failed to set up an OAuth connection")
			continue
		}
		sm, err := NewStreams(ctx, logger, conn)
		if err != nil {
			logger.Error(err, "Failed to create a new stream")
			continue
		}

		// Update the gRPC client and connection in BufferedGrpcWriteSyncer
		bufferedGrpcSyncer.UpdateClient(sm.instance.logStream, sm.instance.conn)
		go bufferedGrpcSyncer.ListenToLogStream()

		err = sm.BootUpStreamAndReconnect(ctx)
		if err != nil {
			logger.Error(err, "Failed to bootup and stream.")
		}
	}
}
