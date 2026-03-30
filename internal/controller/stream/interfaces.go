// Copyright 2026 Illumio, Inc. All Rights Reserved.

// Package stream provides gRPC stream management for CloudSecure communication.
//
// Note: LogStream interface is defined in logging/grpc_logger.go (not here)
// to avoid circular dependency. The logging package's BufferedGrpcWriteSyncer
// requires LogStream, and the stream package imports logging. Moving LogStream
// here would create: stream → logging → stream (circular import).
package stream

import (
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

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
