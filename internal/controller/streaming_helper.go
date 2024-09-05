// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8scluster/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// sendObjectMetaData sends a KubernetesMetadata to CloudSecure into the given stream.
// Its used for the intial boot up of the operator so that is can stream everything currently in the cluster.
func sendObjectMetaData(sm *streamManager, metadata *pb.KubernetesObjectMetadata) error {
	if err := sm.instance.streamKubernetesResources.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_ResourceMetadata{ResourceMetadata: metadata}}); err != nil {
		sm.logger.Errorw("Failed to send resource metadata", "error", err)
		return err
	}
	return nil
}

func sendNetworkFlowsData(sm *streamManager, flow *pb.CiliumFlow) error {
	if err := sm.instance.streamKubernetesFlows.Send(&pb.SendKubernetesNetworkFlowsRequest{Flow: flow}); err != nil {
		sm.logger.Errorw("Failed to send flow metadata", "error", err)
		return err
	}
	return nil
}

// streamMutationObjectMetaData sends a resource mutation message into the given stream.
func streamMutationObjectMetaData(sm *streamManager, metadata *pb.KubernetesObjectMetadata, eventType watch.EventType) error {
	switch eventType {
	case watch.Added:
		if err := sm.instance.streamKubernetesResources.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{KubernetesResourceMutation: &pb.KubernetesResourceMutation{Mutation: &pb.KubernetesResourceMutation_CreateResource{CreateResource: metadata}}}}); err != nil {
			sm.logger.Errorw("Failed to send create resource mutatuion", "error", err)
			return err
		}
	case watch.Deleted:
		if err := sm.instance.streamKubernetesResources.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{KubernetesResourceMutation: &pb.KubernetesResourceMutation{Mutation: &pb.KubernetesResourceMutation_DeleteResource{DeleteResource: metadata}}}}); err != nil {
			sm.logger.Errorw("Failed to send delete resource mutatuion", "error", err)
			return err
		}
	case watch.Modified:
		if err := sm.instance.streamKubernetesResources.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{KubernetesResourceMutation: &pb.KubernetesResourceMutation{Mutation: &pb.KubernetesResourceMutation_UpdateResource{UpdateResource: metadata}}}}); err != nil {
			sm.logger.Errorw("Failed to send update resource mutatuion", "error", err)
			return err
		}
	}
	return nil
}
