// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/kubernetes"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// convertObjectToMetadata extracts the ObjectMeta from a metav1.Object interface.
func convertObjectToMetadata(obj metav1.Object) metav1.ObjectMeta {
	objMetadata := metav1.ObjectMeta{
		Name:            obj.GetName(),
		Namespace:       obj.GetNamespace(),
		UID:             obj.GetUID(),
		ResourceVersion: obj.GetResourceVersion(),
		Labels:          obj.GetLabels(),
		Annotations:     obj.GetAnnotations(),
	}
	return objMetadata
}

// getObjectMetadataFromRuntimeObject safely extracts metadata from any Kubernetes runtime.Object.
// It returns a pointer to a metav1.ObjectMeta structure if successful, along with any error encountered.
func getObjectMetadataFromRuntimeObject(obj runtime.Object) (*metav1.ObjectMeta, error) {
	objectMeta, err := meta.Accessor(obj)
	if err != nil {
		return nil, err
	}
	convertedObjMeta := convertObjectToMetadata(objectMeta)
	return &convertedObjMeta, nil
}

// getMetadatafromResource extracts the metav1.ObjectMeta from an unstructured.Unstructured resource.
// It utilizes the unstructured's inherent methods to access the metadata directly.
func getMetadatafromResource(logger *zap.SugaredLogger, resource unstructured.Unstructured) (*metav1.ObjectMeta, error) {
	// Convert unstructured object to a map.
	itemMap := resource.Object
	// Extract metadata from map.
	if metadata, found := itemMap["metadata"].(map[string]interface{}); found {
		// Convert the metadata map to JSON and then unmarshal into metav1.ObjectMeta.
		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			logger.Errorw("Error marshalling metadata", "error", err)
			return &metav1.ObjectMeta{}, err
		}
		var objectMeta metav1.ObjectMeta
		if err := json.Unmarshal(metadataJSON, &objectMeta); err != nil {
			logger.Errorw("Error unmarshalling metadata", "error", err)
			return &metav1.ObjectMeta{}, err
		}
		return &objectMeta, err
	} else {
		return &metav1.ObjectMeta{}, errors.New("could not grab metadata from a resource")
	}
}

// convertMetaObjectToMetadata takes a metav1.ObjectMeta and converts it into a proto message object KubernetesMetadata.
func convertMetaObjectToMetadata(logger *zap.SugaredLogger, ctx context.Context, obj metav1.ObjectMeta, clientset *kubernetes.Clientset, resource string) (*pb.KubernetesObjectData, error) {
	ownerReferences, err := convertOwnerReferences(obj.GetOwnerReferences())
	if err != nil {
		logger.Errorw("cannot convert OwnerReferences", "error", err)
		return &pb.KubernetesObjectData{}, fmt.Errorf("cannot convert OwnerReferences")
	}
	objMetadata := &pb.KubernetesObjectData{
		Annotations:       obj.GetAnnotations(),
		CreationTimestamp: convertToProtoTimestamp(obj.CreationTimestamp),
		Kind:              resource,
		Labels:            obj.GetLabels(),
		Name:              obj.GetName(),
		Namespace:         obj.GetNamespace(),
		OwnerReferences:   ownerReferences,
		ResourceVersion:   obj.GetResourceVersion(),
		Uid:               string(obj.GetUID()),
	}
	switch resource {
	case "Pod":
		hostIPs, err := getPodIPAddresses(ctx, obj.GetName(), clientset, obj.GetNamespace())
		if err != nil {
			return objMetadata, nil
		}
		objMetadata.KindSpecific = &pb.KubernetesObjectData_Pod{Pod: &pb.KubernetesPodData{IpAddresses: convertHostIPsToStrings(hostIPs)}}
	case "Node":
		providerId, err := getProviderIdNodeSpec(ctx, clientset, obj.GetName())
		if err != nil {
			return objMetadata, nil
		}
		objMetadata.KindSpecific = &pb.KubernetesObjectData_Node{Node: &pb.KubernetesNodeData{ProviderId: providerId}}
	case "Service":
		clusterIp, err := getServiceClusterIp(ctx, obj.GetName(), clientset, obj.GetNamespace())
		if err != nil {
			return objMetadata, nil
		}
		objMetadata.KindSpecific = &pb.KubernetesObjectData_Service{Service: &pb.KubernetesServiceData{ClusterIp: clusterIp}}

	}
	return objMetadata, nil
}

func getServiceClusterIp(ctx context.Context, namespace string, clientset *kubernetes.Clientset, serviceName string) (string, error) {
	service, err := clientset.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return "", errors.New("failed to get service")
	}
	clusterIP := service.Spec.ClusterIP
	if clusterIP == "" {
		return "", errors.New("service does not have a cluster IP")
	}
	return clusterIP, nil
}

func convertOwnerReferences(ownerReferences []metav1.OwnerReference) ([]*pb.KubernetesOwnerReference, error) {
	if len(ownerReferences) == 0 {
		return nil, nil
	}

	result := make([]*pb.KubernetesOwnerReference, 0, len(ownerReferences))
	for _, ownerRef := range ownerReferences {
		// Safely checking for nil values
		var blockOwnerDeletion bool
		if ownerRef.BlockOwnerDeletion != nil {
			blockOwnerDeletion = *ownerRef.BlockOwnerDeletion
		}

		var controller bool
		if ownerRef.Controller != nil {
			controller = *ownerRef.Controller
		}

		k8sOwnerRef := &pb.KubernetesOwnerReference{
			ApiVersion:         ownerRef.APIVersion,
			BlockOwnerDeletion: blockOwnerDeletion,
			Controller:         controller,
			Kind:               ownerRef.Kind,
			Name:               ownerRef.Name,
			Uid:                string(ownerRef.UID),
		}
		result = append(result, k8sOwnerRef)
	}

	return result, nil
}

// getProviderIdNodeSpec uses a node name to return the providerID within the node's spec
func getProviderIdNodeSpec(ctx context.Context, clientset *kubernetes.Clientset, nodeName string) (string, error) {
	node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return "", nil
	}
	if node.Spec.ProviderID != "" {
		return node.Spec.ProviderID, nil
	}
	return "", errors.New("no providerID set")
}

// getPodIPAddresses uses a pod name and namespace to grab the hostIP addresses within the podStatus
func getPodIPAddresses(ctx context.Context, podName string, clientset *kubernetes.Clientset, namespace string) ([]v1.HostIP, error) {
	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		// Could be that the pod no longer exists
		return []v1.HostIP{}, nil
	}
	if pod.Status.HostIPs != nil {
		return pod.Status.HostIPs, nil
	}
	return []v1.HostIP{}, nil
}

// convertHostIPsToStrings converts a slice of v1.HostIP to a slice of strings
func convertHostIPsToStrings(hostIPs []v1.HostIP) []string {
	stringIPs := make([]string, len(hostIPs))
	for i, hostIP := range hostIPs {
		stringIPs[i] = hostIP.IP
	}
	return stringIPs
}

// convertToProtoTimestamp converts a Kubernetes metav1.Time into a Protobuf Timestamp.
func convertToProtoTimestamp(k8sTime metav1.Time) *timestamppb.Timestamp {
	return timestamppb.New(k8sTime.Time)
}
