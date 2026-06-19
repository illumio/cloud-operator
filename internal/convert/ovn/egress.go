// Copyright 2026 Illumio, Inc. All Rights Reserved.

package ovn

import (
	"encoding/json"
	"errors"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"
	k8sUnstructured "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/convert"
)

// IsEgressResource returns true if the input identifies an OpenShift OVN EgressFirewall
// or EgressIP resource.
// Accepts both Kind (PascalCase) and resource name (lowercase plural).
func IsEgressResource(kindOrResource string) bool {
	switch kindOrResource {
	case "EgressFirewall", "EgressIP",
		"egressfirewalls", "egressips":
		return true
	default:
		return false
	}
}

// ConvertUnstructuredToEgressResource converts an unstructured EgressFirewall or EgressIP
// resource to a KubernetesObjectData proto.
func ConvertUnstructuredToEgressResource(obj *k8sUnstructured.Unstructured) (*pb.KubernetesObjectData, error) {
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
		OwnerReferences:   convert.ConvertOwnerReferences(obj.GetOwnerReferences()),
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
	case "EgressFirewall":
		var ef egressFirewall
		if err := json.Unmarshal(jsonBytes, &ef); err != nil {
			return nil, fmt.Errorf("deserializing %s: %w", gvk.Kind, err)
		}

		objMetadata.KindSpecific = &pb.KubernetesObjectData_EgressFirewall{
			EgressFirewall: convertEgressFirewallData(&ef),
		}
	case "EgressIP":
		var eip egressIP
		if err := json.Unmarshal(jsonBytes, &eip); err != nil {
			return nil, fmt.Errorf("deserializing %s: %w", gvk.Kind, err)
		}

		objMetadata.KindSpecific = &pb.KubernetesObjectData_EgressIp{
			EgressIp: convertEgressIPData(&eip),
		}
	default:
		return nil, fmt.Errorf("unsupported Egress resource kind: %s", gvk.Kind)
	}

	return objMetadata, nil
}

// --- EgressFirewall conversion ---

func convertEgressFirewallData(ef *egressFirewall) *pb.KubernetesEgressFirewallData {
	data := &pb.KubernetesEgressFirewallData{}

	if len(ef.Spec.Egress) > 0 {
		data.Egress = convertEgressFirewallRules(ef.Spec.Egress)
	}

	return data
}

func convertEgressFirewallRules(rules []egressFirewallRule) []*pb.EgressFirewallRule {
	result := make([]*pb.EgressFirewallRule, 0, len(rules))
	for i := range rules {
		rule := &rules[i]

		pbRule := &pb.EgressFirewallRule{
			Type: rule.Type,
			To:   convertEgressFirewallDestination(&rule.To),
		}

		if len(rule.Ports) > 0 {
			pbRule.Ports = convertEgressFirewallPorts(rule.Ports)
		}

		result = append(result, pbRule)
	}

	return result
}

func convertEgressFirewallDestination(dst *egressFirewallDestination) *pb.EgressFirewallDestination {
	pbDst := &pb.EgressFirewallDestination{}

	if dst.CIDRSelector != "" {
		pbDst.CidrSelector = &dst.CIDRSelector
	}

	if dst.DNSName != "" {
		pbDst.DnsName = &dst.DNSName
	}

	return pbDst
}

func convertEgressFirewallPorts(ports []egressFirewallPort) []*pb.EgressFirewallPort {
	result := make([]*pb.EgressFirewallPort, 0, len(ports))
	for _, p := range ports {
		result = append(result, &pb.EgressFirewallPort{
			Protocol: p.Protocol,
			Port:     p.Port,
		})
	}

	return result
}

// --- EgressIP conversion ---

func convertEgressIPData(eip *egressIP) *pb.KubernetesEgressIPData {
	data := &pb.KubernetesEgressIPData{}

	if len(eip.Spec.EgressIPs) > 0 {
		data.EgressIps = eip.Spec.EgressIPs
	}

	if eip.Spec.NamespaceSelector != nil {
		data.NamespaceSelector = convert.ConvertLabelSelectorToProto(eip.Spec.NamespaceSelector)
	}

	if eip.Spec.PodSelector != nil {
		data.PodSelector = convert.ConvertLabelSelectorToProto(eip.Spec.PodSelector)
	}

	return data
}
