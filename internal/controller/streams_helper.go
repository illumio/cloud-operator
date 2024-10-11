// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/version"
	"k8s.io/apimachinery/pkg/watch"
)

// sendObjectData sends a KubernetesObjectData to CloudSecure into the given stream.
// Its used for the intial boot up of the operator so that is can stream everything currently in the cluster.
func sendObjectData(sm *streamManager, metadata *pb.KubernetesObjectData) error {
	if err := sm.streamClient.resourceStream.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_ResourceData{ResourceData: metadata}}); err != nil {
		sm.logger.Errorw("Failed to send resource metadata",
			"error", err,
		)
		return err
	}
	return nil
}

func sendCiliumFlow(sm *streamManager, flow *pb.CiliumFlow) error {
	if err := sm.streamClient.networkFlowsStream.Send(&pb.SendKubernetesNetworkFlowsRequest{Request: &pb.SendKubernetesNetworkFlowsRequest_CiliumFlow{CiliumFlow: flow}}); err != nil {
		sm.logger.Errorw("Failed to send network flow",
			"error", err,
		)
		return err
	}
	return nil
}

// streamMutationObjectData sends a resource mutation message into the given stream.
func streamMutationObjectData(sm *streamManager, metadata *pb.KubernetesObjectData, eventType watch.EventType) error {
	switch eventType {
	case watch.Added:
		if err := sm.streamClient.resourceStream.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{KubernetesResourceMutation: &pb.KubernetesResourceMutation{Mutation: &pb.KubernetesResourceMutation_CreateResource{CreateResource: metadata}}}}); err != nil {
			sm.logger.Errorw("Failed to send create resource mutation", "error", err)
			return err
		}
	case watch.Deleted:
		if err := sm.streamClient.resourceStream.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{KubernetesResourceMutation: &pb.KubernetesResourceMutation{Mutation: &pb.KubernetesResourceMutation_DeleteResource{DeleteResource: metadata}}}}); err != nil {
			sm.logger.Errorw("Failed to send delete resource mutation", "error", err)
			return err
		}
	case watch.Modified:
		if err := sm.streamClient.resourceStream.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{KubernetesResourceMutation: &pb.KubernetesResourceMutation{Mutation: &pb.KubernetesResourceMutation_UpdateResource{UpdateResource: metadata}}}}); err != nil {
			sm.logger.Errorw("Failed to send update resource mutation", "error", err)
			return err
		}
	}
	return nil
}

// sendClusterMetadata sends a message to indicate current cluster metadata
func sendClusterMetadata(ctx context.Context, sm *streamManager) error {
	clusterUid, err := GetClusterID(ctx, sm.logger)
	if err != nil {
		sm.logger.Errorw("Error getting cluster id", "error", err)
	}
	clientset, err := NewClientSet()
	if err != nil {
		sm.logger.Errorw("Error creating clientset", "error", err)
	}
	kubernetesVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		sm.logger.Errorw("Error getting Kubernetes version", "error", err)
	}
	if err := sm.streamClient.resourceStream.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_ClusterMetadata{ClusterMetadata: &pb.KubernetesClusterMetadata{Uid: clusterUid, KubernetesVersion: kubernetesVersion.String(), OperatorVersion: version.Version()}}}); err != nil {
		sm.logger.Errorw("Failed to send cluster metadata",
			"error", err,
		)
		return err
	}
	return nil
}

// sendResourceSnapshotComplete sends a message to indicate that the initial inventory snapshot has been completely streamed into the given stream.
func sendResourceSnapshotComplete(sm *streamManager) error {
	if err := sm.streamClient.resourceStream.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_ResourceSnapshotComplete{}}); err != nil {
		sm.logger.Errorw("Falied to send resource snapshot complete",
			"error", err,
		)
		return err
	}
	return nil
}
