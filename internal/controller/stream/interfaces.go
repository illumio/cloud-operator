// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// ConfigStream abstracts the GetConfigurationUpdates gRPC stream.
type ConfigStream interface {
	Send(req *pb.GetConfigurationUpdatesRequest) error
	Recv() (*pb.GetConfigurationUpdatesResponse, error)
}

// ResourceStream abstracts the SendKubernetesResources gRPC stream.
type ResourceStream interface {
	Send(req *pb.SendKubernetesResourcesRequest) error
	Recv() (*pb.SendKubernetesResourcesResponse, error)
}

// NetworkFlowsStream abstracts the SendKubernetesNetworkFlows gRPC stream.
type NetworkFlowsStream interface {
	Send(req *pb.SendKubernetesNetworkFlowsRequest) error
	Recv() (*pb.SendKubernetesNetworkFlowsResponse, error)
}
