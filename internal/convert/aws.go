// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	networkingv1 "k8s.io/api/networking/v1"
	k8sUnstructured "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// IsAWSResource returns true if the input identifies an AWS VPC CNI policy resource.
// Accepts both Kind (PascalCase) and resource name (lowercase plural).
func IsAWSResource(kindOrResource string) bool {
	switch kindOrResource {
	case "ClusterNetworkPolicy", "clusternetworkpolicies",
		"ApplicationNetworkPolicy", "applicationnetworkpolicies":
		return true
	default:
		return false
	}
}

// NewAWSResourceConverter returns a ResourceConverter for AWS VPC CNI policy
// resources. It closes over logger so conversion can warn on unexpected values
// (e.g. unknown policy types) without failing the conversion.
func NewAWSResourceConverter(logger *zap.Logger) func(ctx context.Context, obj *k8sUnstructured.Unstructured) (*pb.KubernetesObjectData, error) {
	return func(_ context.Context, obj *k8sUnstructured.Unstructured) (*pb.KubernetesObjectData, error) {
		return ConvertUnstructuredToAWSResource(logger, obj)
	}
}

// ConvertUnstructuredToAWSResource converts an unstructured AWS ClusterNetworkPolicy
// (networking.k8s.aws/v1alpha1) to a KubernetesObjectData proto.
func ConvertUnstructuredToAWSResource(logger *zap.Logger, obj *k8sUnstructured.Unstructured) (*pb.KubernetesObjectData, error) {
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

	jsonBytes, err := obj.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("marshaling %s to JSON: %w", gvk.Kind, err)
	}

	switch gvk.Kind {
	case "ClusterNetworkPolicy":
		// ClusterNetworkPolicy is cluster-scoped, so no namespace is set.
		var cnp awsClusterNetworkPolicy
		if err := json.Unmarshal(jsonBytes, &cnp); err != nil {
			return nil, fmt.Errorf("deserializing %s: %w", gvk.Kind, err)
		}

		objMetadata.KindSpecific = &pb.KubernetesObjectData_AwsClusterNetworkPolicy{
			AwsClusterNetworkPolicy: convertAWSClusterNetworkPolicy(&cnp),
		}
	case "ApplicationNetworkPolicy":
		// ApplicationNetworkPolicy is namespaced.
		if namespace := obj.GetNamespace(); namespace != "" {
			objMetadata.Namespace = &namespace
		}

		var anp awsApplicationNetworkPolicy
		if err := json.Unmarshal(jsonBytes, &anp); err != nil {
			return nil, fmt.Errorf("deserializing %s: %w", gvk.Kind, err)
		}

		objMetadata.KindSpecific = &pb.KubernetesObjectData_AwsApplicationNetworkPolicy{
			AwsApplicationNetworkPolicy: convertAWSApplicationNetworkPolicy(logger, &anp),
		}
	default:
		return nil, fmt.Errorf("unsupported AWS resource kind: %s", gvk.Kind)
	}

	return objMetadata, nil
}

func convertAWSClusterNetworkPolicy(cnp *awsClusterNetworkPolicy) *pb.KubernetesAWSClusterNetworkPolicyData {
	priority := cnp.Spec.Priority

	return &pb.KubernetesAWSClusterNetworkPolicyData{
		Priority: &priority,
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
		out.Namespaces = convertLabelSelectorToProto(sub.Namespaces)
	}

	return out
}

func convertAWSNamespacedPod(p *awsNamespacedPod) *pb.AWSNetworkPolicyPodSelector {
	if p == nil {
		return nil
	}

	return &pb.AWSNetworkPolicyPodSelector{
		NamespaceSelector: convertLabelSelectorToProto(&p.NamespaceSelector),
		PodSelector:       convertLabelSelectorToProto(&p.PodSelector),
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
			pbPort.PortNumber = &pb.AWSNetworkPolicyPortNumber{
				Protocol: p.PortNumber.Protocol,
				Port:     p.PortNumber.Port,
			}
		}

		if p.PortRange != nil {
			pbPort.PortRange = &pb.AWSNetworkPolicyPortRange{
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
			Namespaces: convertLabelSelectorToProto(p.Namespaces),
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
			Namespaces:  convertLabelSelectorToProto(p.Namespaces),
			Networks:    p.Networks,
			DomainNames: p.DomainNames,
		})
	}

	return out
}

func convertAWSApplicationNetworkPolicy(logger *zap.Logger, anp *awsApplicationNetworkPolicy) *pb.KubernetesAWSApplicationNetworkPolicyData {
	spec := anp.Spec

	return &pb.KubernetesAWSApplicationNetworkPolicyData{
		PodSelector: convertLabelSelectorToProto(&spec.PodSelector),
		PolicyTypes: convertAWSANPPolicyTypes(logger, spec.PolicyTypes),
		Ingress:     convertAWSANPIngressRules(spec.Ingress),
		Egress:      convertAWSANPEgressRules(spec.Egress),
	}
}

// convertAWSANPPolicyTypes copies the policy types through unchanged, warning on
// any value that isn't the expected "Ingress" or "Egress" so unexpected CRD
// values surface in logs instead of silently passing through.
func convertAWSANPPolicyTypes(logger *zap.Logger, policyTypes []string) []string {
	if len(policyTypes) == 0 {
		return nil
	}

	out := make([]string, 0, len(policyTypes))
	for _, pt := range policyTypes {
		if pt != "Ingress" && pt != "Egress" {
			logger.Warn("Unknown ApplicationNetworkPolicy policy type", zap.String("policy_type", pt))
		}

		out = append(out, pt)
	}

	return out
}

// convertAWSANPIngressRules converts standard NetworkPolicy ingress rules. The
// CRD embeds upstream networking/v1 types; ingress peers never carry domain names.
func convertAWSANPIngressRules(rules []networkingv1.NetworkPolicyIngressRule) []*pb.AWSApplicationNetworkPolicyIngressRule {
	if len(rules) == 0 {
		return nil
	}

	out := make([]*pb.AWSApplicationNetworkPolicyIngressRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, &pb.AWSApplicationNetworkPolicyIngressRule{
			From:  convertAWSANPIngressPeers(r.From),
			Ports: convertANPPorts(r.Ports),
		})
	}

	return out
}

func convertAWSANPEgressRules(rules []awsANPEgressRule) []*pb.AWSApplicationNetworkPolicyEgressRule {
	if len(rules) == 0 {
		return nil
	}

	out := make([]*pb.AWSApplicationNetworkPolicyEgressRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, &pb.AWSApplicationNetworkPolicyEgressRule{
			To:    convertAWSANPEgressPeers(r.To),
			Ports: convertANPPorts(r.Ports),
		})
	}

	return out
}

// convertAWSANPIngressPeers converts upstream NetworkPolicyPeers (ingress sources).
func convertAWSANPIngressPeers(peers []networkingv1.NetworkPolicyPeer) []*pb.AWSApplicationNetworkPolicyIngressPeer {
	if len(peers) == 0 {
		return nil
	}

	out := make([]*pb.AWSApplicationNetworkPolicyIngressPeer, 0, len(peers))
	for _, p := range peers {
		out = append(out, &pb.AWSApplicationNetworkPolicyIngressPeer{
			PodSelector:       convertLabelSelectorToProto(p.PodSelector),
			NamespaceSelector: convertLabelSelectorToProto(p.NamespaceSelector),
			IpBlock:           convertIPBlockToProto(p.IPBlock),
		})
	}

	return out
}

// convertAWSANPEgressPeers converts ApplicationNetworkPolicy egress destinations,
// which additionally carry FQDN domain names.
func convertAWSANPEgressPeers(peers []awsANPEgressPeer) []*pb.AWSApplicationNetworkPolicyEgressPeer {
	if len(peers) == 0 {
		return nil
	}

	out := make([]*pb.AWSApplicationNetworkPolicyEgressPeer, 0, len(peers))
	for _, p := range peers {
		out = append(out, &pb.AWSApplicationNetworkPolicyEgressPeer{
			PodSelector:       convertLabelSelectorToProto(p.PodSelector),
			NamespaceSelector: convertLabelSelectorToProto(p.NamespaceSelector),
			IpBlock:           convertIPBlockToProto(p.IPBlock),
			DomainNames:       p.DomainNames,
		})
	}

	return out
}

// convertANPPorts converts upstream NetworkPolicyPorts to the AWS ANP port proto.
// The port field is a google.protobuf.Value so protojson emits a bare number for
// numeric ports and a bare string for named ports, matching the CRD's intstr.IntOrString.
func convertANPPorts(ports []networkingv1.NetworkPolicyPort) []*pb.AWSApplicationNetworkPolicyPort {
	if len(ports) == 0 {
		return nil
	}

	out := make([]*pb.AWSApplicationNetworkPolicyPort, 0, len(ports))
	for _, p := range ports {
		pbPort := &pb.AWSApplicationNetworkPolicyPort{}

		if p.Protocol != nil {
			protocol := string(*p.Protocol)
			pbPort.Protocol = &protocol
		}

		if p.Port != nil {
			switch p.Port.Type {
			case intstr.Int:
				pbPort.Port = structpb.NewNumberValue(float64(p.Port.IntVal))
			case intstr.String:
				pbPort.Port = structpb.NewStringValue(p.Port.StrVal)
			}
		}

		if p.EndPort != nil {
			pbPort.EndPort = p.EndPort
		}

		out = append(out, pbPort)
	}

	return out
}
