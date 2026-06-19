// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

import (
	"encoding/json"
	"errors"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"
	k8sUnstructured "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// IsAdminNetworkPolicyResource returns true if the input identifies an AdminNetworkPolicy
// or BaselineAdminNetworkPolicy resource.
// Accepts both Kind (PascalCase) and resource name (lowercase plural).
func IsAdminNetworkPolicyResource(kindOrResource string) bool {
	switch kindOrResource {
	case "AdminNetworkPolicy", "BaselineAdminNetworkPolicy",
		"adminnetworkpolicies", "baselineadminnetworkpolicies":
		return true
	default:
		return false
	}
}

// ConvertUnstructuredToAdminNetworkPolicyResource converts an unstructured AdminNetworkPolicy
// or BaselineAdminNetworkPolicy resource to a KubernetesObjectData proto.
func ConvertUnstructuredToAdminNetworkPolicyResource(obj *k8sUnstructured.Unstructured) (*pb.KubernetesObjectData, error) {
	if obj == nil {
		return nil, errors.New("cannot convert nil object")
	}

	gvk := obj.GroupVersionKind()
	namespace := obj.GetNamespace()

	objMetadata := &pb.KubernetesObjectData{
		Annotations:       obj.GetAnnotations(),
		ApiGroup:          gvk.Group,
		ApiVersion:        gvk.Version,
		CreationTimestamp: timestamppb.New(obj.GetCreationTimestamp().Time),
		Kind:              gvk.Kind,
		Labels:            obj.GetLabels(),
		Name:              obj.GetName(),
		OwnerReferences:   convertOwnerReferences(obj.GetOwnerReferences()),
		ResourceVersion:   obj.GetResourceVersion(),
		Uid:               string(obj.GetUID()),
	}

	if namespace != "" {
		objMetadata.Namespace = &namespace
	}

	jsonBytes, err := obj.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("marshaling %s to JSON: %w", gvk.Kind, err)
	}

	switch gvk.Kind {
	case "AdminNetworkPolicy":
		var anp adminNetworkPolicy
		if err := json.Unmarshal(jsonBytes, &anp); err != nil {
			return nil, fmt.Errorf("deserializing %s: %w", gvk.Kind, err)
		}

		objMetadata.KindSpecific = &pb.KubernetesObjectData_AdminNetworkPolicy{
			AdminNetworkPolicy: convertAdminNetworkPolicyData(&anp),
		}
	case "BaselineAdminNetworkPolicy":
		var banp baselineAdminNetworkPolicy
		if err := json.Unmarshal(jsonBytes, &banp); err != nil {
			return nil, fmt.Errorf("deserializing %s: %w", gvk.Kind, err)
		}

		objMetadata.KindSpecific = &pb.KubernetesObjectData_BaselineAdminNetworkPolicy{
			BaselineAdminNetworkPolicy: convertBaselineAdminNetworkPolicyData(&banp),
		}
	default:
		return nil, fmt.Errorf("unsupported AdminNetworkPolicy resource kind: %s", gvk.Kind)
	}

	return objMetadata, nil
}

func convertAdminNetworkPolicyData(anp *adminNetworkPolicy) *pb.KubernetesAdminNetworkPolicyData {
	data := &pb.KubernetesAdminNetworkPolicyData{
		Priority: anp.Spec.Priority,
		Subject:  convertANPSubject(&anp.Spec.Subject),
	}

	if len(anp.Spec.Ingress) > 0 {
		data.Ingress = convertANPIngressRules(anp.Spec.Ingress)
	}

	if len(anp.Spec.Egress) > 0 {
		data.Egress = convertANPEgressRules(anp.Spec.Egress)
	}

	return data
}

func convertBaselineAdminNetworkPolicyData(banp *baselineAdminNetworkPolicy) *pb.KubernetesBaselineAdminNetworkPolicyData {
	data := &pb.KubernetesBaselineAdminNetworkPolicyData{
		Subject: convertANPSubject(&banp.Spec.Subject),
	}

	if len(banp.Spec.Ingress) > 0 {
		data.Ingress = convertBaselineANPIngressRules(banp.Spec.Ingress)
	}

	if len(banp.Spec.Egress) > 0 {
		data.Egress = convertBaselineANPEgressRules(banp.Spec.Egress)
	}

	return data
}

// --- Subject conversion ---

func convertANPSubject(subject *adminNetworkPolicySubject) *pb.AdminNetworkPolicySubject {
	pbSubject := &pb.AdminNetworkPolicySubject{}

	if subject.Namespaces != nil {
		pbSubject.Namespaces = convertLabelSelectorToProto(subject.Namespaces)
	}

	if subject.Pods != nil {
		pbSubject.Pods = convertNamespacedPod(subject.Pods)
	}

	return pbSubject
}

func convertNamespacedPod(pod *namespacedPod) *pb.AdminNetworkPolicyNamespacedPod {
	return &pb.AdminNetworkPolicyNamespacedPod{
		NamespaceSelector: convertLabelSelectorToProto(&pod.NamespaceSelector),
		PodSelector:       convertLabelSelectorToProto(&pod.PodSelector),
	}
}

// --- Ingress / Egress rule conversion ---

func convertANPIngressRules(rules []adminNetworkPolicyIngressRule) []*pb.AdminNetworkPolicyRule {
	result := make([]*pb.AdminNetworkPolicyRule, 0, len(rules))
	for _, rule := range rules {
		pbRule := &pb.AdminNetworkPolicyRule{
			Action: rule.Action,
		}

		if rule.Name != "" {
			pbRule.Name = &rule.Name
		}

		for _, peer := range rule.From {
			pbRule.Peers = append(pbRule.Peers, convertANPIngressPeer(&peer))
		}

		if rule.Ports != nil {
			pbRule.Ports = convertANPPorts(*rule.Ports)
		}

		result = append(result, pbRule)
	}

	return result
}

func convertANPEgressRules(rules []adminNetworkPolicyEgressRule) []*pb.AdminNetworkPolicyRule {
	result := make([]*pb.AdminNetworkPolicyRule, 0, len(rules))
	for _, rule := range rules {
		pbRule := &pb.AdminNetworkPolicyRule{
			Action: rule.Action,
		}

		if rule.Name != "" {
			pbRule.Name = &rule.Name
		}

		for _, peer := range rule.To {
			pbRule.Peers = append(pbRule.Peers, convertANPEgressPeer(&peer))
		}

		if rule.Ports != nil {
			pbRule.Ports = convertANPPorts(*rule.Ports)
		}

		result = append(result, pbRule)
	}

	return result
}

func convertBaselineANPIngressRules(rules []baselineAdminNetworkPolicyIngressRule) []*pb.AdminNetworkPolicyRule {
	result := make([]*pb.AdminNetworkPolicyRule, 0, len(rules))
	for _, rule := range rules {
		pbRule := &pb.AdminNetworkPolicyRule{
			Action: rule.Action,
		}

		if rule.Name != "" {
			pbRule.Name = &rule.Name
		}

		for _, peer := range rule.From {
			pbRule.Peers = append(pbRule.Peers, convertANPIngressPeer(&peer))
		}

		if rule.Ports != nil {
			pbRule.Ports = convertANPPorts(*rule.Ports)
		}

		result = append(result, pbRule)
	}

	return result
}

func convertBaselineANPEgressRules(rules []baselineAdminNetworkPolicyEgressRule) []*pb.AdminNetworkPolicyRule {
	result := make([]*pb.AdminNetworkPolicyRule, 0, len(rules))
	for _, rule := range rules {
		pbRule := &pb.AdminNetworkPolicyRule{
			Action: rule.Action,
		}

		if rule.Name != "" {
			pbRule.Name = &rule.Name
		}

		for _, peer := range rule.To {
			pbRule.Peers = append(pbRule.Peers, convertANPEgressPeer(&peer))
		}

		if rule.Ports != nil {
			pbRule.Ports = convertANPPorts(*rule.Ports)
		}

		result = append(result, pbRule)
	}

	return result
}

// --- Peer conversion ---

func convertANPIngressPeer(peer *adminNetworkPolicyIngressPeer) *pb.AdminNetworkPolicyPeer {
	pbPeer := &pb.AdminNetworkPolicyPeer{}

	if peer.Namespaces != nil {
		pbPeer.Namespaces = convertLabelSelectorToProto(peer.Namespaces)
	}

	if peer.Pods != nil {
		pbPeer.Pods = convertNamespacedPod(peer.Pods)
	}

	return pbPeer
}

func convertANPEgressPeer(peer *adminNetworkPolicyEgressPeer) *pb.AdminNetworkPolicyPeer {
	pbPeer := &pb.AdminNetworkPolicyPeer{}

	if peer.Namespaces != nil {
		pbPeer.Namespaces = convertLabelSelectorToProto(peer.Namespaces)
	}

	if peer.Pods != nil {
		pbPeer.Pods = convertNamespacedPod(peer.Pods)
	}

	if peer.Nodes != nil {
		pbPeer.Nodes = convertLabelSelectorToProto(peer.Nodes)
	}

	if len(peer.Networks) > 0 {
		pbPeer.Networks = peer.Networks
	}

	if len(peer.DomainNames) > 0 {
		pbPeer.DomainNames = peer.DomainNames
	}

	return pbPeer
}

// --- Port conversion ---

func convertANPPorts(ports []adminNetworkPolicyPort) []*pb.AdminNetworkPolicyPort {
	if len(ports) == 0 {
		return nil
	}

	result := make([]*pb.AdminNetworkPolicyPort, 0, len(ports))
	for _, p := range ports {
		pbPort := &pb.AdminNetworkPolicyPort{}

		if p.PortNumber != nil {
			pbPort.PortNumber = &pb.AdminNetworkPolicyPortNumber{
				Protocol: p.PortNumber.Protocol,
				Port:     p.PortNumber.Port,
			}
		}

		if p.NamedPort != nil {
			pbPort.NamedPort = p.NamedPort
		}

		if p.PortRange != nil {
			pbPort.PortRange = &pb.AdminNetworkPolicyPortRange{
				Protocol: p.PortRange.Protocol,
				Start:    p.PortRange.Start,
				End:      p.PortRange.End,
			}
		}

		result = append(result, pbPort)
	}

	return result
}
