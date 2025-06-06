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
func getMetadatafromResource(logger *zap.Logger, resource unstructured.Unstructured) (*metav1.ObjectMeta, error) {
	// Convert unstructured object to a map.
	itemMap := resource.Object
	// Extract metadata from map.
	if metadata, found := itemMap["metadata"].(map[string]interface{}); found {
		// Convert the metadata map to JSON and then unmarshal into metav1.ObjectMeta.
		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			logger.Error("Error marshalling metadata", zap.Error(err))
			return &metav1.ObjectMeta{}, err
		}
		var objectMeta metav1.ObjectMeta
		if err := json.Unmarshal(metadataJSON, &objectMeta); err != nil {
			logger.Error("Error unmarshalling metadata", zap.Error(err))
			return &metav1.ObjectMeta{}, err
		}
		return &objectMeta, err
	} else {
		return &metav1.ObjectMeta{}, errors.New("could not grab metadata from a resource")
	}
}

// convertMetaObjectToMetadata takes a metav1.ObjectMeta and converts it into a proto message object KubernetesMetadata.
func convertMetaObjectToMetadata(logger *zap.Logger, ctx context.Context, obj metav1.ObjectMeta, clientset *kubernetes.Clientset, resource string) (*pb.KubernetesObjectData, error) {
	ownerReferences, err := convertOwnerReferences(obj.GetOwnerReferences())
	if err != nil {
		logger.Error("cannot convert OwnerReferences", zap.Error(err))
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
		podIPS, err := getPodIPAddresses(ctx, obj.GetName(), clientset, obj.GetNamespace())
		if err != nil {
			return objMetadata, nil
		}
		objMetadata.KindSpecific = &pb.KubernetesObjectData_Pod{Pod: &pb.KubernetesPodData{IpAddresses: convertPodIPsToStrings(podIPS)}}
	case "Node":
		providerId, err := getProviderIdNodeSpec(ctx, clientset, obj.GetName())
		if err != nil {
			return objMetadata, nil
		}
		ipAddresses, err := getNodeIpAddresses(ctx, clientset, obj.GetName())
		if err != nil {
			return objMetadata, nil
		}
		objMetadata.KindSpecific = &pb.KubernetesObjectData_Node{Node: &pb.KubernetesNodeData{ProviderId: providerId, IpAddresses: ipAddresses}}
	case "Service":
		convertedServiceData, err := convertToKubernetesServiceData(ctx, obj.GetName(), clientset, obj.GetNamespace())
		if err != nil {
			return objMetadata, nil
		}
		objMetadata.KindSpecific = &pb.KubernetesObjectData_Service{Service: convertedServiceData}
	}
	return objMetadata, nil
}

// getNodeIpAddresses fetches the IP addresses of a node
func getNodeIpAddresses(ctx context.Context, clientset *kubernetes.Clientset, nodeName string) ([]string, error) {
	node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, errors.New("failed to get node")
	}
	ipAddresses := []string{}
	for _, address := range node.Status.Addresses {
		// We are excluding hostnames
		if address.Type == v1.NodeInternalIP || address.Type == v1.NodeExternalIP {
			ipAddresses = append(ipAddresses, address.Address)
		}
	}
	return ipAddresses, nil
}

// convertIngressToStringList converts an array of v1.LoadBalancerIngress to a string array
func convertIngressToStringList(ingresses []v1.LoadBalancerIngress) []string {
	result := []string{}
	for _, ingress := range ingresses {
		if ingress.IP != "" {
			result = append(result, ingress.IP)
		}
		if ingress.Hostname != "" {
			result = append(result, ingress.Hostname)
		}
	}
	return result
}

// convertServicePortsToPorts coverts an array of v1.ServicePort objects to a proto message KubernetesServiceData_ServicePort
func convertServicePortsToPorts(servicePorts []v1.ServicePort) []*pb.KubernetesServiceData_ServicePort {
	ports := make([]*pb.KubernetesServiceData_ServicePort, 0, len(servicePorts))
	for _, sp := range servicePorts {
		protocol := string(sp.Protocol)
		if protocol == "" {
			protocol = string(v1.ProtocolTCP)
		}
		port := &pb.KubernetesServiceData_ServicePort{
			Port:     uint32(sp.Port),
			Protocol: protocol,
		}
		if sp.NodePort != 0 {
			port.NodePort = int32ToUint32(&sp.NodePort)
		}
		ports = append(ports, port)
	}
	return ports
}

// int32ToUint32 converts *int32 to *uint32
func int32ToUint32(i *int32) *uint32 {
	if i == nil {
		return nil
	}
	val := uint32(*i)
	return &val
}

// Combine all IPs into a single list
func combineIPAddresses(clusterIps []string, externalIps []string, loadBalancerIngresses []string, loadBalancerIp string) []string {
	combinedIPs := make([]string, 0, len(clusterIps)+len(externalIps)+len(loadBalancerIngresses)+1)
	combinedIPs = append(combinedIPs, clusterIps...)
	combinedIPs = append(combinedIPs, externalIps...)
	combinedIPs = append(combinedIPs, loadBalancerIngresses...)
	if loadBalancerIp != "" {
		combinedIPs = append(combinedIPs, loadBalancerIp)
	}
	return combinedIPs
}

// Convert ServiceAttributes to KubernetesServiceData
func convertToKubernetesServiceData(ctx context.Context, serviceName string, clientset *kubernetes.Clientset, namespace string) (*pb.KubernetesServiceData, error) {
	service, err := clientset.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return nil, errors.New("failed to get service")
	}
	loadBalancerIngress := []string{}
	if len(service.Status.LoadBalancer.Ingress) > 0 {
		loadBalancerIngress = convertIngressToStringList(service.Status.LoadBalancer.Ingress)
	}
	ports := convertServicePortsToPorts(service.Spec.Ports)
	// Combine all IPs
	combinedIPs := combineIPAddresses(service.Spec.ClusterIPs, service.Spec.ExternalIPs, loadBalancerIngress, service.Spec.LoadBalancerIP)

	// Convert NodePorts to ServicePorts
	servicePorts := make([]*pb.KubernetesServiceData_ServicePort, len(ports))
	for i, np := range ports {
		var nodePort *uint32
		if np.NodePort != nil {
			nodePortValue := uint32(*np.NodePort)
			nodePort = &nodePortValue
		}
		servicePorts[i] = &pb.KubernetesServiceData_ServicePort{
			NodePort:          nodePort,
			Port:              uint32(np.Port),
			Protocol:          np.Protocol,
			LoadBalancerPorts: np.LoadBalancerPorts,
		}
	}

	return &pb.KubernetesServiceData{
		IpAddresses:       combinedIPs,
		Ports:             servicePorts,
		Type:              string(service.Spec.Type),
		ExternalName:      &service.Spec.ExternalName,
		LoadBalancerClass: service.Spec.LoadBalancerClass,
	}, nil
}

// convertOwnerReferences converts a slice of Kubernetes OwnerReference objects into a slice of
// protobuf KubernetesOwnerReference objects.
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
func getPodIPAddresses(ctx context.Context, podName string, clientset *kubernetes.Clientset, namespace string) ([]v1.PodIP, error) {
	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		// Could be that the pod no longer exists
		return []v1.PodIP{}, nil
	}
	if pod.Status.PodIPs != nil {
		return pod.Status.PodIPs, nil
	}
	return []v1.PodIP{}, nil
}

// convertPodIPsToStrings converts a slice of v1.PodIP to a slice of strings
func convertPodIPsToStrings(podIPs []v1.PodIP) []string {
	stringIPs := make([]string, len(podIPs))
	for i, podIP := range podIPs {
		stringIPs[i] = podIP.IP
	}
	return stringIPs
}

// convertToProtoTimestamp converts a Kubernetes metav1.Time into a Protobuf Timestamp.
func convertToProtoTimestamp(k8sTime metav1.Time) *timestamppb.Timestamp {
	return timestamppb.New(k8sTime.Time)
}
