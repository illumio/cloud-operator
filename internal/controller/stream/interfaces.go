// Copyright 2026 Illumio, Inc. All Rights Reserved.

// Package stream provides gRPC stream management for CloudSecure communication.
//
// Note: LogStream interface is defined in logging/grpc_logger.go (not here)
// to avoid circular dependency. The logging package's BufferedGrpcWriteSyncer
// requires LogStream, and the stream package imports logging. Moving LogStream
// here would create: stream → logging → stream (circular import).
package stream

import (
	"context"

	"k8s.io/client-go/kubernetes"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// StreamClient abstracts all stream operations.
// Each stream type (config, logs, resources, flows) implements this interface.
// The stream manager only needs this interface - no knowledge of protobuf types.
type StreamClient interface {
	// Run sends/receives messages over a gRPC stream until the stream is closed
	// or the context is canceled. This is the main loop for the stream.
	Run(ctx context.Context) error

	// SendKeepalive sends a keepalive message over the gRPC stream.
	SendKeepalive(ctx context.Context) error

	// Close gracefully closes the stream.
	Close() error
}

// StreamClientFactory creates StreamClients.
// Configuration is passed via factory fields, set by cmd/main.go.
// This enables dependency injection and testability.
type StreamClientFactory interface {
	// NewStreamClient creates a new StreamClient connected to the given gRPC client.
	NewStreamClient(ctx context.Context, grpcClient pb.KubernetesInfoServiceClient) (StreamClient, error)

	// Name returns the name of the stream for logging purposes.
	Name() string
}

// ConfigurationStream abstracts the GetConfigurationUpdates gRPC stream.
type ConfigurationStream interface {
	Send(req *pb.GetConfigurationUpdatesRequest) error
	Recv() (*pb.GetConfigurationUpdatesResponse, error)
}

// KubernetesResourcesStream abstracts the SendKubernetesResources gRPC stream.
type KubernetesResourcesStream interface {
	Send(req *pb.SendKubernetesResourcesRequest) error
	Recv() (*pb.SendKubernetesResourcesResponse, error)
}

// KubernetesNetworkFlowsStream abstracts the SendKubernetesNetworkFlows gRPC stream.
type KubernetesNetworkFlowsStream interface {
	Send(req *pb.SendKubernetesNetworkFlowsRequest) error
	Recv() (*pb.SendKubernetesNetworkFlowsResponse, error)
}

// K8sClientGetter provides access to Kubernetes client.
type K8sClientGetter interface {
	GetClientset() kubernetes.Interface
}

// K8sClientSetter allows setting the Kubernetes client on a factory.
// Used by factories that need K8s client but are created before the client exists.
type K8sClientSetter interface {
	SetK8sClient(client K8sClientGetter)
}

// FlowCollectorSetter allows setting the flow collector type on a factory.
// Used by resources factory to report the correct flow collector in cluster metadata.
type FlowCollectorSetter interface {
	SetFlowCollector(collectorName string)
}

// ConnSetter allows setting the gRPC connection on a factory.
// Used by logs factory to pass the connection to BufferedGrpcWriteSyncer.
type ConnSetter interface {
	SetConn(conn any)
}
