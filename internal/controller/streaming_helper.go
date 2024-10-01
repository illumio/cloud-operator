// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// sendObjectData sends a KubernetesObjectData to CloudSecure into the given stream.
// Its used for the intial boot up of the operator so that is can stream everything currently in the cluster.
func sendObjectData(sm *streamManager, data *pb.KubernetesObjectData) error {
	if err := sm.streamClient.streamKubernetesResources.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_ResourceData{ResourceData: data}}); err != nil {
		sm.logger.Errorw("Failed to send resource data",
			"error", err,
		)
		return err
	}
	return nil
}

func sendCiliumFlow(sm *streamManager, flow *pb.CiliumFlow) error {
	if err := sm.streamClient.streamKubernetesFlows.Send(&pb.SendKubernetesNetworkFlowsRequest{Request: &pb.SendKubernetesNetworkFlowsRequest_CiliumFlow{CiliumFlow: flow}}); err != nil {
		sm.logger.Errorw("Failed to send network flow",
			"error", err,
		)
		return err
	}
	return nil
}

// streamMutationObjectData sends a resource mutation message into the given stream.
func streamMutationObjectData(sm *streamManager, data *pb.KubernetesObjectData, eventType watch.EventType) error {
	switch eventType {
	case watch.Added:
		if err := sm.streamClient.streamKubernetesResources.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{KubernetesResourceMutation: &pb.KubernetesResourceMutation{Mutation: &pb.KubernetesResourceMutation_CreateResource{CreateResource: data}}}}); err != nil {
			sm.logger.Errorw("Failed to send create resource mutation", "error", err)
			return err
		}
	case watch.Deleted:
		if err := sm.streamClient.streamKubernetesResources.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{KubernetesResourceMutation: &pb.KubernetesResourceMutation{Mutation: &pb.KubernetesResourceMutation_DeleteResource{DeleteResource: data}}}}); err != nil {
			sm.logger.Errorw("Failed to send delete resource mutation", "error", err)
			return err
		}
	case watch.Modified:
		if err := sm.streamClient.streamKubernetesResources.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{KubernetesResourceMutation: &pb.KubernetesResourceMutation{Mutation: &pb.KubernetesResourceMutation_UpdateResource{UpdateResource: data}}}}); err != nil {
			sm.logger.Errorw("Failed to send update resource mutation", "error", err)
			return err
		}
	}
	return nil
}
