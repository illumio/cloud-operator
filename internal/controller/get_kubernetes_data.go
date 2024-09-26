// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// convertMetaObjectToMetadata takes a metav1.ObjectMeta and converts it into a proto message object KubernetesMetadata.
func convertMetaObjectToMetadata(obj metav1.ObjectMeta, resource string) *pb.KubernetesObjectMetadata {
	objMetadata := &pb.KubernetesObjectMetadata{
		Annotations:       obj.GetAnnotations(),
		CreationTimestamp: convertToProtoTimestamp(obj.CreationTimestamp),
		Kind:              resource,
		Labels:            obj.GetLabels(),
		Name:              obj.GetName(),
		Namespace:         obj.GetNamespace(),
		ResourceVersion:   obj.GetResourceVersion(),
		Uid:               string(obj.GetUID()),
	}
	return objMetadata
}

// convertToProtoTimestamp converts a Kubernetes metav1.Time into a Protobuf Timestamp.
func convertToProtoTimestamp(k8sTime metav1.Time) *timestamppb.Timestamp {
	return timestamppb.New(k8sTime.Time)
}
