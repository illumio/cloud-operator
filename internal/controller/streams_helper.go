package controller

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/watch"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/version"
)

// Helper function to send a request to the resource stream
func (sm *streamManager) sendToResourceStream(logger *zap.Logger, request *pb.SendKubernetesResourcesRequest) error {
	if err := sm.streamClient.resourceStream.Send(request); err != nil {
		logger.Error("Failed to send request", zap.Stringer("request", request), zap.Error(err))
		return err
	}
	return nil
}

// sendObjectData sends a KubernetesObjectData to CloudSecure into the given stream.
// Its used for the intial boot up of the operator so that is can stream everything currently in the cluster.
func (sm *streamManager) sendObjectData(logger *zap.Logger, metadata *pb.KubernetesObjectData) error {
	request := &pb.SendKubernetesResourcesRequest{
		Request: &pb.SendKubernetesResourcesRequest_ResourceData{
			ResourceData: metadata,
		},
	}
	return sm.sendToResourceStream(logger, request)
}

// sendNetworkFlowRequest sends a network flow to the networkFlowsStream
func (sm *streamManager) sendNetworkFlowRequest(logger *zap.Logger, flow interface{}) error {
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
	if err := sm.streamClient.networkFlowsStream.Send(request); err != nil {
		logger.Error("Failed to send network flow", zap.Error(err))
		return err
	}
	return nil
}

// streamMutationObjectData does type gymnastics then sends the result over the
// wire. It "upgrades" a KubernetesObjectData into a KubernetesResourceMutation
// (which can be sent over the wire). It needs to use information from the
// watch.EventType to accomplish this
func (sm *streamManager) streamMutationObjectData(logger *zap.Logger, metadata *pb.KubernetesObjectData, eventType watch.EventType) error {
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
	return sm.sendToResourceStream(logger, request)
}

// sendClusterMetadata sends a message to indicate current cluster metadata
func (sm *streamManager) sendClusterMetadata(ctx context.Context, logger *zap.Logger) error {
	clusterUid, err := GetClusterID(ctx, logger)
	if err != nil {
		logger.Error("Error getting cluster id", zap.Error(err))
		return err
	}
	clientset, err := NewClientSet()
	if err != nil {
		logger.Error("Error creating clientset", zap.Error(err))
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
				FlowCollector:     sm.streamClient.flowCollector,
			},
		},
	}
	return sm.sendToResourceStream(logger, request)
}

// sendResourceSnapshotComplete sends a message to indicate that the initial inventory snapshot has been completely streamed into the given stream.
func (sm *streamManager) sendResourceSnapshotComplete(logger *zap.Logger) error {
	request := &pb.SendKubernetesResourcesRequest{
		Request: &pb.SendKubernetesResourcesRequest_ResourceSnapshotComplete{},
	}
	return sm.sendToResourceStream(logger, request)
}

// sendKeepalive accepts a stream type & sends a keepalive ping on that stream
func (sm *streamManager) sendKeepalive(logger *zap.Logger, st StreamType) error {
	var err error

	switch st {
	case STREAM_NETWORK_FLOWS:
		err = sm.streamClient.networkFlowsStream.Send(&pb.SendKubernetesNetworkFlowsRequest{
			Request: &pb.SendKubernetesNetworkFlowsRequest_Keepalive{
				Keepalive: &pb.Keepalive{},
			},
		})
	case STREAM_RESOURCES:
		err = sm.streamClient.resourceStream.Send(&pb.SendKubernetesResourcesRequest{
			Request: &pb.SendKubernetesResourcesRequest_Keepalive{
				Keepalive: &pb.Keepalive{},
			},
		})
	case STREAM_LOGS:
		err = sm.streamClient.logStream.Send(&pb.SendLogsRequest{
			Request: &pb.SendLogsRequest_Keepalive{
				Keepalive: &pb.Keepalive{},
			},
		})
	case STREAM_CONFIGURATION:
		err = sm.streamClient.configStream.Send(&pb.GetConfigurationUpdatesRequest{
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
