// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/watch"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/auth"
	"github.com/illumio/cloud-operator/internal/version"
)

// ResourceProcessingTimeout is the maximum time allowed for resource processing before
// the server is considered unhealthy.
const ResourceProcessingTimeout = 5 * time.Minute

// SendToResourceStream sends a request to the resource stream.
func (sm *Manager) SendToResourceStream(logger *zap.Logger, request *pb.SendKubernetesResourcesRequest) error {
	if err := sm.Client.KubernetesResourcesStream.Send(request); err != nil {
		logger.Error("Failed to send request", zap.Stringer("request", request), zap.Error(err))

		return err
	}

	return nil
}

// SendObjectData sends a KubernetesObjectData to CloudSecure.
func (sm *Manager) SendObjectData(logger *zap.Logger, metadata *pb.KubernetesObjectData) error {
	request := &pb.SendKubernetesResourcesRequest{
		Request: &pb.SendKubernetesResourcesRequest_ResourceData{
			ResourceData: metadata,
		},
	}

	return sm.SendToResourceStream(logger, request)
}

// SendNetworkFlowRequest sends a network flow to the networkFlowsStream.
func (sm *Manager) SendNetworkFlowRequest(logger *zap.Logger, flow any) error {
	var request *pb.SendKubernetesNetworkFlowsRequest

	switch f := flow.(type) {
	case *pb.FiveTupleFlow:
		request = &pb.SendKubernetesNetworkFlowsRequest{
			Request: &pb.SendKubernetesNetworkFlowsRequest_FiveTupleFlow{
				FiveTupleFlow: f,
			},
		}
	case *pb.CiliumFlow:
		request = &pb.SendKubernetesNetworkFlowsRequest{
			Request: &pb.SendKubernetesNetworkFlowsRequest_CiliumFlow{
				CiliumFlow: f,
			},
		}
	default:
		return fmt.Errorf("unsupported flow type: %T", flow)
	}

	if err := sm.Client.KubernetesNetworkFlowsStream.Send(request); err != nil {
		logger.Error("Failed to send network flow", zap.Error(err))

		return err
	}

	return nil
}

// CreateMutationObject converts a KubernetesObjectData into a KubernetesResourceMutation.
func (sm *Manager) CreateMutationObject(metadata *pb.KubernetesObjectData, eventType watch.EventType) *pb.KubernetesResourceMutation {
	var mutation *pb.KubernetesResourceMutation

	switch eventType {
	case watch.Added:
		mutation = &pb.KubernetesResourceMutation{
			Mutation: &pb.KubernetesResourceMutation_CreateResource{
				CreateResource: metadata,
			},
		}
	case watch.Deleted:
		mutation = &pb.KubernetesResourceMutation{
			Mutation: &pb.KubernetesResourceMutation_DeleteResource{
				DeleteResource: metadata,
			},
		}
	case watch.Modified:
		mutation = &pb.KubernetesResourceMutation{
			Mutation: &pb.KubernetesResourceMutation_UpdateResource{
				UpdateResource: metadata,
			},
		}
	case watch.Bookmark:
	case watch.Error:
	}

	return mutation
}

// SendClusterMetadata sends cluster metadata to CloudSecure.
func (sm *Manager) SendClusterMetadata(ctx context.Context, logger *zap.Logger) error {
	clientset := sm.K8sClient.GetClientset()

	clusterUid, err := auth.GetClusterID(ctx, logger, clientset)
	if err != nil {
		logger.Error("Error getting cluster id", zap.Error(err))

		return err
	}

	kubernetesVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		logger.Error("Error getting Kubernetes version", zap.Error(err))

		return err
	}

	request := &pb.SendKubernetesResourcesRequest{
		Request: &pb.SendKubernetesResourcesRequest_ClusterMetadata{
			ClusterMetadata: &pb.KubernetesClusterMetadata{
				Uid:               clusterUid,
				KubernetesVersion: kubernetesVersion.String(),
				OperatorVersion:   version.Version(),
				FlowCollector:     sm.Client.FlowCollector,
			},
		},
	}

	return sm.SendToResourceStream(logger, request)
}

// SendResourceSnapshotComplete sends a message indicating the initial snapshot is complete.
func (sm *Manager) SendResourceSnapshotComplete(logger *zap.Logger) error {
	request := &pb.SendKubernetesResourcesRequest{
		Request: &pb.SendKubernetesResourcesRequest_ResourceSnapshotComplete{},
	}

	return sm.SendToResourceStream(logger, request)
}

// SendKeepalive sends a keepalive ping on the specified stream.
func (sm *Manager) SendKeepalive(logger *zap.Logger, st Type) error {
	var err error

	switch st {
	case TypeNetworkFlows:
		err = sm.Client.KubernetesNetworkFlowsStream.Send(&pb.SendKubernetesNetworkFlowsRequest{
			Request: &pb.SendKubernetesNetworkFlowsRequest_Keepalive{
				Keepalive: &pb.Keepalive{},
			},
		})
	case TypeResources:
		err = sm.Client.KubernetesResourcesStream.Send(&pb.SendKubernetesResourcesRequest{
			Request: &pb.SendKubernetesResourcesRequest_Keepalive{
				Keepalive: &pb.Keepalive{},
			},
		})
	case TypeLogs:
		err = sm.Client.LogStream.Send(&pb.SendLogsRequest{
			Request: &pb.SendLogsRequest_Keepalive{
				Keepalive: &pb.Keepalive{},
			},
		})
	case TypeConfiguration:
		err = sm.Client.ConfigurationStream.Send(&pb.GetConfigurationUpdatesRequest{
			Request: &pb.GetConfigurationUpdatesRequest_Keepalive{
				Keepalive: &pb.Keepalive{},
			},
		})
	default:
		return fmt.Errorf("unsupported stream type: %s", st)
	}

	if err != nil {
		logger.Error("Failed to send keepalive on stream", zap.Error(err))

		return err
	}

	return nil
}

// ServerIsHealthy checks if a deadlock has occurred within the resource listing process.
func ServerIsHealthy() bool {
	dd.mutex.RLock()
	defer dd.mutex.RUnlock()

	if dd.processingResources && time.Since(dd.timeStarted) > ResourceProcessingTimeout {
		return false
	}

	return true
}
