package controller

import (
	"context"
	"fmt"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/version"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/watch"
)

// Helper function to send a request to the resource stream
func sendToResourceStream(logger *zap.Logger, stream pb.KubernetesInfoService_SendKubernetesResourcesClient, request *pb.SendKubernetesResourcesRequest) error {
	if err := stream.Send(request); err != nil {
		logger.Error("Failed to send request", zap.Stringer("request", request), zap.Error(err))
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

// sendNetworkFlowRequest sends a network flow to the networkFlowsStream
func sendNetworkFlowRequest(sm *streamManager, flow interface{}) error {
	var request *pb.SendKubernetesNetworkFlowsRequest

	switch f := flow.(type) {
	case *pb.FalcoFlow:
		request = &pb.SendKubernetesNetworkFlowsRequest{
			Request: &pb.SendKubernetesNetworkFlowsRequest_FalcoFlow{
				FalcoFlow: f,
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
	if err := sm.streamClient.networkFlowsStream.Send(request); err != nil {
		sm.logger.Error("Failed to send network flow", zap.Error(err))
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
		sm.logger.Error("Error getting cluster id", zap.Error(err))
		return err
	}
	clientset, err := NewClientSet()
	if err != nil {
		sm.logger.Error("Error creating clientset", zap.Error(err))
		return err
	}
	kubernetesVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		sm.logger.Error("Error getting Kubernetes version", zap.Error(err))
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

// sendKeepaliveNetworkFlow is a `sendKeepalive*` function that simply
// formulates a keepalive ping for the specific stream & sends it
func sendKeepaliveNetworkFlow(sm *streamManager) error {
	request := &pb.SendKubernetesNetworkFlowsRequest{Request: &pb.SendKubernetesNetworkFlowsRequest_KeepalivePing{}}
	if err := sm.streamClient.networkFlowsStream.Send(request); err != nil {
		sm.logger.Error("Failed to send keepalive for network flow", zap.Error(err))
		return err
	}
	return nil
}

// sendKeepaliveResource is a `sendKeepalive*` function that simply
// formulates a keepalive ping for the specific stream & sends it
func sendKeepaliveResource(sm *streamManager) error {
	request := &pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_KeepalivePing{}}
	if err := sm.streamClient.resourceStream.Send(request); err != nil {
		sm.logger.Error("Failed to send keepalive for network flow", zap.Error(err))
		return err
	}
	return nil
}

// sendKeepaliveLog is a `sendKeepalive*` function that simply
// formulates a keepalive ping for the specific stream & sends it
func sendKeepaliveLog(sm *streamManager) error {
	request := &pb.SendLogsRequest{Request: &pb.SendLogsRequest_KeepalivePing{}}
	if err := sm.streamClient.logStream.Send(request); err != nil {
		sm.logger.Error("Failed to send keepalive for network flow", zap.Error(err))
		return err
	}
	return nil
}
