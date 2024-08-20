// Copyright 2024 Illumio, Inc. All Rights Reserved.
package controller

import (
	"context"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8scluster/v1"
	"github.com/illumio/cloud-operator/internal/version"
	"k8s.io/apimachinery/pkg/watch"
)

// sendObjectMetaData sends a KubernetesMetadata to CloudSecure into the given stream.
// Its used for the intial boot up of the operator so that is can stream everything currently in the cluster.
func sendObjectMetaData(sm *streamManager, metadata *pb.KubernetesObjectMetadata) error {
	if err := sm.instance.stream.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_ResourceMetadata{ResourceMetadata: metadata}}); err != nil {
		sm.logger.Error(err, "Failed to send resource metadata")
		return err
	}
	return nil
}

// streamMutationObjectMetaData sends a resource mutation message into the given stream.
func streamMutationObjectMetaData(sm *streamManager, metadata *pb.KubernetesObjectMetadata, eventType watch.EventType) error {
	switch eventType {
	case watch.Added:
		if err := sm.instance.stream.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{KubernetesResourceMutation: &pb.KubernetesResourceMutation{Mutation: &pb.KubernetesResourceMutation_CreateResource{CreateResource: metadata}}}}); err != nil {
			sm.logger.Error(err, "Failed to send create resource mutation")
			return err
		}
	case watch.Deleted:
		if err := sm.instance.stream.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{KubernetesResourceMutation: &pb.KubernetesResourceMutation{Mutation: &pb.KubernetesResourceMutation_DeleteResource{DeleteResource: metadata}}}}); err != nil {
			sm.logger.Error(err, "Failed to send delete resource mutation")
			return err
		}
	case watch.Modified:
		if err := sm.instance.stream.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{KubernetesResourceMutation: &pb.KubernetesResourceMutation{Mutation: &pb.KubernetesResourceMutation_UpdateResource{UpdateResource: metadata}}}}); err != nil {
			sm.logger.Error(err, "Failed to send update resource mutation")
			return err
		}
	}
	return nil
}

// sendResourceSnapshotComplete sends a message to indicate that the initial inventory snapshot has been completely streamed into the given stream.
func (rm *ResourceManager) sendResourceSnapshotComplete() error {
	if err := rm.streamManager.instance.stream.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_ResourceSnapshotComplete{}}); err != nil {
		rm.logger.Error(err, "Falied to send resource snapshot complete")
		return err
	}
	return nil
}

// sendClusterMetadata sends a message to indicate current cluster metadata
func (rm *ResourceManager) sendClusterMetadata(ctx context.Context) error {
	clusterUid, err := GetClusterID(ctx, rm.logger)
	if err != nil {
		rm.logger.Error(err, "Error getting cluster id")
	}
	clientset, err := NewClientSet()
	if err != nil {
		rm.logger.Error(err, "Error creating clientset")
	}
	kubernetesVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		rm.logger.Error(err, "Error getting Kubernetes version")
	}
	if err := rm.streamManager.instance.stream.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_ClusterMetadata{ClusterMetadata: &pb.KubernetesClusterMetadata{Uid: clusterUid, KubernetesVersion: kubernetesVersion.String(), OperatorVersion: version.Version()}}}); err != nil {
		rm.logger.Error(err, "Failed to send cluster metadata")
		return err
	}
	return nil
}
