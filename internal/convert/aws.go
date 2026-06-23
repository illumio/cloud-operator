// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

import (
	"encoding/json"
	"errors"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sUnstructured "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// IsAWSResource returns true if the input identifies an AWS VPC CNI policy resource.
// Accepts both Kind (PascalCase) and resource name (lowercase plural).
func IsAWSResource(kindOrResource string) bool {
	switch kindOrResource {
	case "ClusterNetworkPolicy", "clusternetworkpolicies":
		return true
	default:
		return false
	}
}

// ConvertUnstructuredToAWSResource converts an unstructured AWS ClusterNetworkPolicy
// (networking.k8s.aws/v1alpha1) to a KubernetesObjectData proto.
func ConvertUnstructuredToAWSResource(obj *k8sUnstructured.Unstructured) (*pb.KubernetesObjectData, error) {
	if obj == nil {
		return nil, errors.New("cannot convert nil object")
	}

	gvk := obj.GroupVersionKind()

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
	// ClusterNetworkPolicy is cluster-scoped, so no namespace is set.

	jsonBytes, err := obj.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("marshaling %s to JSON: %w", gvk.Kind, err)
	}

	switch gvk.Kind {
	case "ClusterNetworkPolicy":
		var cnp awsClusterNetworkPolicy
		if err := json.Unmarshal(jsonBytes, &cnp); err != nil {
			return nil, fmt.Errorf("deserializing %s: %w", gvk.Kind, err)
		}

		objMetadata.KindSpecific = &pb.KubernetesObjectData_AwsClusterNetworkPolicy{
			AwsClusterNetworkPolicy: convertAWSClusterNetworkPolicy(&cnp),
		}
	default:
		return nil, fmt.Errorf("unsupported AWS resource kind: %s", gvk.Kind)
	}

	return objMetadata, nil
}

func convertAWSClusterNetworkPolicy(cnp *awsClusterNetworkPolicy) *pb.KubernetesAWSClusterNetworkPolicyData {
	return &pb.KubernetesAWSClusterNetworkPolicyData{
		Priority: cnp.Spec.Priority,
		Tier:     cnp.Spec.Tier,
		Subject:  convertAWSSubject(cnp.Spec.Subject),
		Ingress:  convertAWSIngressRules(cnp.Spec.Ingress),
		Egress:   convertAWSEgressRules(cnp.Spec.Egress),
	}
}

func convertAWSSubject(sub awsSubject) *pb.AWSNetworkPolicySubject {
	out := &pb.AWSNetworkPolicySubject{}

	if sub.Pods != nil {
		out.Pods = convertAWSNamespacedPod(sub.Pods)
	}

	if sub.Namespaces != nil {
		out.Namespaces = convertMetaV1LabelSelector(sub.Namespaces)
	}

	return out
}

func convertAWSNamespacedPod(p *awsNamespacedPod) *pb.AWSNetworkPolicyPodSelector {
	if p == nil {
		return nil
	}

	return &pb.AWSNetworkPolicyPodSelector{
		NamespaceSelector: convertMetaV1LabelSelector(&p.NamespaceSelector),
		PodSelector:       convertMetaV1LabelSelector(&p.PodSelector),
	}
}

func convertAWSIngressRules(rules []awsIngressRule) []*pb.AWSNetworkPolicyIngressRule {
	if len(rules) == 0 {
		return nil
	}

	out := make([]*pb.AWSNetworkPolicyIngressRule, 0, len(rules))
	for _, r := range rules {
		pbRule := &pb.AWSNetworkPolicyIngressRule{
			Action: r.Action,
			Ports:  convertAWSPorts(r.Ports),
			From:   convertAWSIngressPeers(r.From),
		}

		if r.Name != "" {
			name := r.Name
			pbRule.Name = &name
		}

		out = append(out, pbRule)
	}

	return out
}

func convertAWSEgressRules(rules []awsEgressRule) []*pb.AWSNetworkPolicyEgressRule {
	if len(rules) == 0 {
		return nil
	}

	out := make([]*pb.AWSNetworkPolicyEgressRule, 0, len(rules))
	for _, r := range rules {
		pbRule := &pb.AWSNetworkPolicyEgressRule{
			Action: r.Action,
			Ports:  convertAWSPorts(r.Ports),
			To:     convertAWSEgressPeers(r.To),
		}

		if r.Name != "" {
			name := r.Name
			pbRule.Name = &name
		}

		out = append(out, pbRule)
	}

	return out
}

func convertAWSPorts(ports *[]awsPort) []*pb.AWSNetworkPolicyPort {
	if ports == nil || len(*ports) == 0 {
		return nil
	}

	out := make([]*pb.AWSNetworkPolicyPort, 0, len(*ports))
	for _, p := range *ports {
		pbPort := &pb.AWSNetworkPolicyPort{}

		if p.PortNumber != nil {
			pbPort.PortNumber = &pb.AWSPortNumber{
				Protocol: p.PortNumber.Protocol,
				Port:     p.PortNumber.Port,
			}
		}

		if p.PortRange != nil {
			pbPort.PortRange = &pb.AWSPortRange{
				Protocol: p.PortRange.Protocol,
				Start:    p.PortRange.Start,
				End:      p.PortRange.End,
			}
		}

		if p.NamedPort != nil {
			np := *p.NamedPort
			pbPort.NamedPort = &np
		}

		out = append(out, pbPort)
	}

	return out
}

func convertAWSIngressPeers(peers []awsIngressPeer) []*pb.AWSNetworkPolicyIngressPeer {
	if len(peers) == 0 {
		return nil
	}

	out := make([]*pb.AWSNetworkPolicyIngressPeer, 0, len(peers))
	for _, p := range peers {
		out = append(out, &pb.AWSNetworkPolicyIngressPeer{
			Pods:       convertAWSNamespacedPod(p.Pods),
			Namespaces: convertMetaV1LabelSelector(p.Namespaces),
		})
	}

	return out
}

func convertAWSEgressPeers(peers []awsEgressPeer) []*pb.AWSNetworkPolicyEgressPeer {
	if len(peers) == 0 {
		return nil
	}

	out := make([]*pb.AWSNetworkPolicyEgressPeer, 0, len(peers))
	for _, p := range peers {
		out = append(out, &pb.AWSNetworkPolicyEgressPeer{
			Pods:        convertAWSNamespacedPod(p.Pods),
			Namespaces:  convertMetaV1LabelSelector(p.Namespaces),
			Networks:    p.Networks,
			DomainNames: p.DomainNames,
		})
	}

	return out
}

// convertMetaV1LabelSelector converts a standard metav1.LabelSelector to proto.
// Cilium's path uses slim label-selector types; the AWS CRD uses the standard
// metav1 type, so this is a small dedicated helper.
func convertMetaV1LabelSelector(sel *metav1.LabelSelector) *pb.LabelSelector {
	if sel == nil {
		return nil
	}

	out := &pb.LabelSelector{}

	if len(sel.MatchLabels) > 0 {
		out.MatchLabels = sel.MatchLabels
	}

	for _, e := range sel.MatchExpressions {
		out.MatchExpressions = append(out.MatchExpressions, &pb.LabelSelectorRequirement{
			Key:      e.Key,
			Operator: string(e.Operator),
			Values:   e.Values,
		})
	}

	return out
}
