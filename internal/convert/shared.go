// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

import (
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// ConvertOwnerReferences converts a slice of Kubernetes OwnerReference objects into a slice of
// protobuf KubernetesOwnerReference objects. It is exported for use by CRD-specific converter
// subpackages (e.g. aws, cilium, ovn).
func ConvertOwnerReferences(ownerReferences []metav1.OwnerReference) []*pb.KubernetesOwnerReference {
	return convertOwnerReferences(ownerReferences)
}

// ConvertLabelSelectorToProto converts a Kubernetes LabelSelector to a proto LabelSelector.
// It is exported for use by CRD-specific converter subpackages (e.g. aws, cilium, ovn).
func ConvertLabelSelectorToProto(selector *metav1.LabelSelector) *pb.LabelSelector {
	return convertLabelSelectorToProto(selector)
}

// ConvertIPBlockToProto converts a Kubernetes IPBlock to a proto IPBlock object.
// It is exported for use by CRD-specific converter subpackages (e.g. aws, cilium, ovn).
func ConvertIPBlockToProto(ipBlock *networkingv1.IPBlock) *pb.IPBlock {
	return convertIPBlockToProto(ipBlock)
}
