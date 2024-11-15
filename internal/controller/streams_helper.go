package controller

import (
	"context"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/version"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/watch"
)

// Helper function to send a request to the resource stream
func sendToResourceStream(logger *zap.SugaredLogger, stream pb.KubernetesInfoService_SendKubernetesResourcesClient, request *pb.SendKubernetesResourcesRequest) error {
	if err := stream.Send(request); err != nil {
		logger.Errorw("Failed to send request", "request", request, "error", err)
		return err
	}
	return nil
}

// sendObjectData sends a KubernetesObjectData to CloudSecure into the given stream.
// Its used for the intial boot up of the operator so that is can stream everything currently in the cluster.
func sendObjectData(sm *streamManager, metadata *pb.KubernetesObjectData) error {
	request := &pb.SendKubernetesResourcesRequest{
		Request: &pb.SendKubernetesResourcesRequest_ResourceData{
			ResourceData: metadata,
		},
	}
	return sendToResourceStream(sm.logger, sm.streamClient.resourceStream, request)
}

// sendFalcoFlow sends a FalcoFlow to the networkFlowsStream
func sendFalcoFlow(sm *streamManager, flow *pb.FalcoFlow) error {
	request := &pb.SendKubernetesNetworkFlowsRequest{
		Request: &pb.SendKubernetesNetworkFlowsRequest_FalcoFlow{
			FalcoFlow: flow,
		},
	}
	if err := sm.streamClient.networkFlowsStream.Send(request); err != nil {
		sm.logger.Errorw("Failed to send network flow", "error", err)
		return err
	}
	return nil
}

// sendCiliumFlow sends a CiliumFlow to the networkFlowsStream
func sendCiliumFlow(sm *streamManager, flow *pb.CiliumFlow) error {
	request := &pb.SendKubernetesNetworkFlowsRequest{
		Request: &pb.SendKubernetesNetworkFlowsRequest_CiliumFlow{
			CiliumFlow: flow,
		},
	}
	if err := sm.streamClient.networkFlowsStream.Send(request); err != nil {
		sm.logger.Errorw("Failed to send network flow", "error", err)
		return err
	}
	return nil
}

// streamMutationObjectData sends a resource mutation message into the given stream.
func streamMutationObjectData(sm *streamManager, metadata *pb.KubernetesObjectData, eventType watch.EventType) error {
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
	}
	request := &pb.SendKubernetesResourcesRequest{
		Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{
			KubernetesResourceMutation: mutation,
		},
	}
	return sendToResourceStream(sm.logger, sm.streamClient.resourceStream, request)
}

// sendClusterMetadata sends a message to indicate current cluster metadata
func sendClusterMetadata(ctx context.Context, sm *streamManager) error {
	clusterUid, err := GetClusterID(ctx, sm.logger)
	if err != nil {
		sm.logger.Errorw("Error getting cluster id", "error", err)
		return err
	}
	clientset, err := NewClientSet()
	if err != nil {
		sm.logger.Errorw("Error creating clientset", "error", err)
		return err
	}
	kubernetesVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		sm.logger.Errorw("Error getting Kubernetes version", "error", err)
		return err
	}
	request := &pb.SendKubernetesResourcesRequest{
		Request: &pb.SendKubernetesResourcesRequest_ClusterMetadata{
			ClusterMetadata: &pb.KubernetesClusterMetadata{
				Uid:               clusterUid,
				KubernetesVersion: kubernetesVersion.String(),
				OperatorVersion:   version.Version(),
			},
		},
	}
	return sendToResourceStream(sm.logger, sm.streamClient.resourceStream, request)
}

// sendResourceSnapshotComplete sends a message to indicate that the initial inventory snapshot has been completely streamed into the given stream.
func sendResourceSnapshotComplete(sm *streamManager) error {
	request := &pb.SendKubernetesResourcesRequest{
		Request: &pb.SendKubernetesResourcesRequest_ResourceSnapshotComplete{},
	}
	return sendToResourceStream(sm.logger, sm.streamClient.resourceStream, request)
}
