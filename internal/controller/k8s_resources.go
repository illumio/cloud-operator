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
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
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
	case "NetworkPolicy":
		networkPolicy, err := getContentsOfNetworkPolicy(ctx, obj.GetName(), clientset, obj.GetNamespace())
		if err != nil {
			return objMetadata, nil
		}
		objMetadata.KindSpecific = &pb.KubernetesObjectData_NetworkPolicy{NetworkPolicy: networkPolicy}
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

// getContentsOfNetworkPolicy gets the contents of a NetworkPolicy and returns it as a KubernetesNetworkPolicyData proto message
func getContentsOfNetworkPolicy(ctx context.Context, networkPolicyName string, clientset *kubernetes.Clientset, networkPolicyNamespace string) (*pb.KubernetesNetworkPolicyData, error) {
	networkPolicy, err := clientset.NetworkingV1().NetworkPolicies(networkPolicyNamespace).Get(ctx, networkPolicyName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	convertedNetworkPolicyData, err := convertNetworkPolicyToProto(networkPolicy)
	if err != nil {
		return nil, err
	}
	return convertedNetworkPolicyData, nil
}

// convertNetworkPolicyToProto converts a Kubernetes NetworkPolicy to a proto message KubernetesNetworkPolicyData
func convertNetworkPolicyToProto(networkPolicy *networkingv1.NetworkPolicy) (*pb.KubernetesNetworkPolicyData, error) {
	if networkPolicy == nil {
		return nil, errors.New("networkPolicy is nil")
	}

	var ingressRules []*pb.NetworkPolicyRule
	if len(networkPolicy.Spec.Ingress) > 0 {
		ingressRules = convertNetworkPolicyIngressRuleToProto(networkPolicy.Spec.Ingress)
	}

	var egressRules []*pb.NetworkPolicyRule
	if len(networkPolicy.Spec.Egress) > 0 {
		egressRules = convertNetworkPolicyEgressRuleToProto(networkPolicy.Spec.Egress)
	}

	var podSelector *pb.LabelSelector
	if len(networkPolicy.Spec.PodSelector.MatchLabels) > 0 || len(networkPolicy.Spec.PodSelector.MatchExpressions) > 0 {
		podSelector = convertLabelSelectorToProto(&networkPolicy.Spec.PodSelector)
	}

	networkPolicyData := &pb.KubernetesNetworkPolicyData{
		PodSelector:  podSelector,
		IngressRules: ingressRules,
		EgressRules:  egressRules,
		Ingress:      len(ingressRules) > 0,
		Egress:       len(egressRules) > 0,
	}

	return networkPolicyData, nil
}

// convertLabelSelectorToProto converts a Kubernetes LabelSelector to a proto message LabelSelector
func convertLabelSelectorToProto(podSelector *metav1.LabelSelector) *pb.LabelSelector {
	if podSelector == nil {
		return nil
	}
	return &pb.LabelSelector{
		MatchLabels:      podSelector.MatchLabels,
		MatchExpressions: convertLabelSelectorRequirementsToProto(podSelector.MatchExpressions),
	}
}

// convertLabelSelectorRequirementsToProto converts a Kubernetes LabelSelectorRequirement to a proto message LabelSelectorRequirement
func convertLabelSelectorRequirementsToProto(requirements []metav1.LabelSelectorRequirement) []*pb.LabelSelectorRequirement {
	if len(requirements) == 0 {
		return nil
	}
	protoRequirements := make([]*pb.LabelSelectorRequirement, 0, len(requirements))
	for _, req := range requirements {
		protoRequirements = append(protoRequirements, &pb.LabelSelectorRequirement{
			Key: req.Key,
			// Operator represents a key's relationship to a set of values. Valid operators are In, NotIn, Exists and DoesNotExist.
			Operator: string(req.Operator),
			Values:   req.Values,
		})
	}
	return protoRequirements
}

// convertNetworkPolicyIngressRuleToProto converts a Kubernetes NetworkPolicyIngressRule to a proto message NetworkPolicyRule
func convertNetworkPolicyIngressRuleToProto(ingressRules []networkingv1.NetworkPolicyIngressRule) []*pb.NetworkPolicyRule {
	if len(ingressRules) == 0 {
		return nil
	}
	protoRules := make([]*pb.NetworkPolicyRule, 0, len(ingressRules))
	for _, rule := range ingressRules {
		protoRules = append(protoRules, &pb.NetworkPolicyRule{
			Peers: convertNetworkPolicyPeerToProto(rule.From),
			Ports: convertNetworkPolicyPortToProto(rule.Ports),
		})
	}
	return protoRules
}

// convertNetworkPolicyEgressRuleToProto converts a Kubernetes NetworkPolicyEgressRule to a proto message NetworkPolicyRule
func convertNetworkPolicyEgressRuleToProto(egressRules []networkingv1.NetworkPolicyEgressRule) []*pb.NetworkPolicyRule {
	if len(egressRules) == 0 {
		return nil
	}
	protoRules := make([]*pb.NetworkPolicyRule, 0, len(egressRules))
	for _, rule := range egressRules {
		protoRules = append(protoRules, &pb.NetworkPolicyRule{
			Peers: convertNetworkPolicyPeerToProto(rule.To),
			Ports: convertNetworkPolicyPortToProto(rule.Ports),
		})
	}
	return protoRules
}

// convertNetworkPolicyPeerToProto converts a Kubernetes NetworkPolicyPeer to a proto message Peer
func convertNetworkPolicyPeerToProto(peers []networkingv1.NetworkPolicyPeer) []*pb.Peer {
	if len(peers) == 0 {
		return nil
	}
	protoPeers := make([]*pb.Peer, 0, len(peers))
	for _, peer := range peers {
		if peer.IPBlock != nil {
			protoPeers = append(protoPeers, &pb.Peer{
				Peer: &pb.Peer_IpBlock{IpBlock: convertIPBlockToProto(peer.IPBlock)},
			})
			continue
		}

		if peer.NamespaceSelector == nil && peer.PodSelector == nil {
			continue
		}

		protoPeers = append(protoPeers, &pb.Peer{
			Peer: &pb.Peer_Pod{
				Pod: &pb.PeerSelector{
					NamespaceSelector: convertLabelSelectorToProto(peer.NamespaceSelector),
					PodSelector:       convertLabelSelectorToProto(peer.PodSelector),
				},
			},
		})
	}

	return protoPeers
}

// convertNetworkPolicyPortToProto converts a Kubernetes NetworkPolicyPort to a proto message Port
func convertNetworkPolicyPortToProto(ports []networkingv1.NetworkPolicyPort) []*pb.Port {
	if len(ports) == 0 {
		return nil
	}
	protoPorts := make([]*pb.Port, 0, len(ports))
	for _, port := range ports {
		protoPort := &pb.Port{
			Protocol: convertProtocolToProto(port.Protocol),
		}
		if port.Port != nil {
			protoPort.Port = &port.Port.StrVal
		}
		convertedEndPort := convertEndPortToProto(port.EndPort)
		if convertedEndPort != nil {
			protoPort.EndPort = convertedEndPort
		}
		protoPorts = append(protoPorts, protoPort)
	}

	return protoPorts
}

// convertProtocolToProto converts a Kubernetes Protocol to a proto Protocol object.
func convertProtocolToProto(protocol *v1.Protocol) pb.Port_Protocol {
	if protocol == nil {
		return pb.Port_PROTOCOL_TCP_UNSPECIFIED
	}
	switch *protocol {
	case v1.ProtocolUDP:
		return pb.Port_PROTOCOL_UDP
	case v1.ProtocolSCTP:
		return pb.Port_PROTOCOL_SCTP
	default:
		return pb.Port_PROTOCOL_TCP_UNSPECIFIED
	}
}

// convertEndPortToProto converts a Kubernetes EndPort to a proto EndPort object.
func convertEndPortToProto(endPort *int32) *int32 {
	if endPort == nil {
		return nil
	}
	return endPort
}

// convertIPBlockToProto converts a Kubernetes IPBlock to a proto IPBlock object.
func convertIPBlockToProto(iPBlock *networkingv1.IPBlock) *pb.IPBlock {
	if iPBlock == nil {
		return nil
	}
	return &pb.IPBlock{
		Cidr:   iPBlock.CIDR,
		Except: iPBlock.Except,
	}
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
