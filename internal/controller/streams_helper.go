// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// sendObjectMetaData sends a KubernetesMetadata to CloudSecure into the given stream.
// Its used for the intial boot up of the operator so that is can stream everything currently in the cluster.
func sendObjectMetaData(sm *streamManager, metadata *pb.KubernetesObjectMetadata) error {
	if err := sm.streamClient.resourceStream.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_ResourceMetadata{ResourceMetadata: metadata}}); err != nil {
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

// streamMutationObjectMetaData sends a resource mutation message into the given stream.
func streamMutationObjectMetaData(sm *streamManager, metadata *pb.KubernetesObjectMetadata, eventType watch.EventType) error {
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
