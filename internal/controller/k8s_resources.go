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

// Assuming ServiceAttributes has appropriate fields
type ServiceAttributes struct {
	ClusterIPs          []string
	ExternalIPs         []string
	LoadBalancerIP      string
	LoadBalancerIngress []string
	LoadBalancerClass   string
	NodePorts           []*Ports
	ExternalName        string
	ServiceType         string // make sure you have this field to capture the service type.
}

type Ports struct {
	NodePort          int32
	Port              int32
	Protocol          string
	LoadBalancerPorts []string
}

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
		serviceAttributes, err := getServiceAttributes(ctx, obj.GetName(), clientset, obj.GetNamespace())
		if err != nil {
			return objMetadata, nil
		}
		convertedServiceData := convertToKubernetesServiceData(serviceAttributes)
		objMetadata.KindSpecific = &pb.KubernetesObjectData_Service{Service: convertedServiceData}

	}
	return objMetadata, nil
}

func convertIngressToStringList(ingress []v1.LoadBalancerIngress) []string {
	result := []string{}
	for _, i := range ingress {
		if i.IP != "" {
			result = append(result, i.IP)
		}
		if i.Hostname != "" {
			result = append(result, i.Hostname)
		}
	}
	return result
}

func convertServicePortsToNodePorts(servicePorts []v1.ServicePort) []*Ports {
	nodePorts := make([]*Ports, 0, len(servicePorts))
	for _, sp := range servicePorts {
		if sp.NodePort != 0 {
			nodePort := &Ports{
				NodePort: sp.NodePort,
				Port:     sp.Port,
				Protocol: string(sp.Protocol),
			}
			nodePorts = append(nodePorts, nodePort)
		}
	}
	return nodePorts
}

func getServiceAttributes(ctx context.Context, serviceName string, clientset *kubernetes.Clientset, namespace string) (*ServiceAttributes, error) {
	service, err := clientset.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return nil, errors.New("failed to get service")
	}
	loadBalancerIngress := []string{}
	if len(service.Status.LoadBalancer.Ingress) > 0 {
		loadBalancerIngress = convertIngressToStringList(service.Status.LoadBalancer.Ingress)
	}
	loadBalancerClass := ""
	if service.Spec.LoadBalancerClass != nil {
		loadBalancerClass = *service.Spec.LoadBalancerClass
	}
	attributes := &ServiceAttributes{
		ClusterIPs:          service.Spec.ClusterIPs,
		ExternalIPs:         service.Spec.ExternalIPs,
		LoadBalancerIngress: loadBalancerIngress,
		LoadBalancerIP:      service.Spec.LoadBalancerIP,
		LoadBalancerClass:   loadBalancerClass,
	}

	if service.Spec.Type == v1.ServiceTypeNodePort || service.Spec.Type == v1.ServiceTypeLoadBalancer {
		attributes.NodePorts = convertServicePortsToNodePorts(service.Spec.Ports)
	}

	// Only set ExternalName if the service type is ExternalName
	if service.Spec.Type == v1.ServiceTypeExternalName {
		attributes.ExternalName = service.Spec.ExternalName
	}

	return attributes, nil
}

// Combine all IPs into a single list
func combineIPAddresses(attributes *ServiceAttributes) []string {
	combinedIPs := []string{}
	combinedIPs = append(combinedIPs, attributes.ClusterIPs...)
	combinedIPs = append(combinedIPs, attributes.ExternalIPs...)
	combinedIPs = append(combinedIPs, attributes.LoadBalancerIngress...)
	if attributes.LoadBalancerIP != "" {
		combinedIPs = append(combinedIPs, attributes.LoadBalancerIP)
	}
	return combinedIPs
}

// Convert ServiceAttributes to KubernetesServiceData
func convertToKubernetesServiceData(attributes *ServiceAttributes) *pb.KubernetesServiceData {
	// Combine all IPs
	combinedIPs := combineIPAddresses(attributes)

	// Convert NodePorts to ServicePorts
	servicePorts := make([]*pb.KubernetesServiceData_ServicePorts, len(attributes.NodePorts))
	for i, np := range attributes.NodePorts {
		servicePorts[i] = &pb.KubernetesServiceData_ServicePorts{
			NodePort:          int32(np.NodePort),
			Port:              int32(np.Port),
			Protocol:          np.Protocol,
			LoadBalencerPorts: np.LoadBalancerPorts,
		}
	}

	return &pb.KubernetesServiceData{
		IpAddresses: combinedIPs,
		Ports:       servicePorts,
		ServiceType: attributes.ServiceType,
	}
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
