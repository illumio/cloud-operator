// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// IsCiliumPolicy returns true if the resource kind is a Cilium network policy.
func IsCiliumPolicy(kind string) bool {
	return kind == "CiliumNetworkPolicy" || kind == "CiliumClusterwideNetworkPolicy"
}

// ConvertUnstructuredToCiliumPolicy converts an unstructured Cilium policy to a KubernetesObjectData proto.
// This handles both CiliumNetworkPolicy and CiliumClusterwideNetworkPolicy.
func ConvertUnstructuredToCiliumPolicy(obj *unstructured.Unstructured) *pb.KubernetesObjectData {
	if obj == nil {
		return nil
	}

	kind := obj.GetKind()

	objMetadata := &pb.KubernetesObjectData{
		Annotations:       obj.GetAnnotations(),
		CreationTimestamp: timestamppb.New(obj.GetCreationTimestamp().Time),
		Kind:              kind,
		Labels:            obj.GetLabels(),
		Name:              obj.GetName(),
		Namespace:         obj.GetNamespace(),
		OwnerReferences:   convertOwnerReferences(obj.GetOwnerReferences()),
		ResourceVersion:   obj.GetResourceVersion(),
		Uid:               string(obj.GetUID()),
	}

	spec, found, err := unstructured.NestedMap(obj.Object, "spec")
	if err != nil || !found {
		return objMetadata
	}

	switch kind {
	case "CiliumNetworkPolicy":
		ciliumPolicy := convertCiliumNetworkPolicySpec(spec)
		objMetadata.KindSpecific = &pb.KubernetesObjectData_CiliumNetworkPolicy{CiliumNetworkPolicy: ciliumPolicy}
	case "CiliumClusterwideNetworkPolicy":
		ciliumPolicy := convertCiliumClusterwideNetworkPolicySpec(spec)
		objMetadata.KindSpecific = &pb.KubernetesObjectData_CiliumClusterwideNetworkPolicy{CiliumClusterwideNetworkPolicy: ciliumPolicy}
	}

	return objMetadata
}

// convertCiliumNetworkPolicySpec converts a CiliumNetworkPolicy spec to proto.
func convertCiliumNetworkPolicySpec(spec map[string]any) *pb.KubernetesCiliumNetworkPolicyData {
	policy := &pb.KubernetesCiliumNetworkPolicyData{}

	// Extract endpoint selector
	if endpointSelector, found, _ := unstructured.NestedMap(spec, "endpointSelector"); found {
		policy.EndpointSelector = convertCiliumLabelSelector(endpointSelector)
	}

	// Extract ingress rules
	if ingress, found, _ := unstructured.NestedSlice(spec, "ingress"); found {
		policy.IngressRules = convertCiliumRules(ingress)
	}

	// Extract egress rules
	if egress, found, _ := unstructured.NestedSlice(spec, "egress"); found {
		policy.EgressRules = convertCiliumRules(egress)
	}

	// Extract ingress deny rules
	if ingressDeny, found, _ := unstructured.NestedSlice(spec, "ingressDeny"); found {
		policy.IngressDenyRules = convertCiliumRules(ingressDeny)
	}

	// Extract egress deny rules
	if egressDeny, found, _ := unstructured.NestedSlice(spec, "egressDeny"); found {
		policy.EgressDenyRules = convertCiliumRules(egressDeny)
	}

	return policy
}

// convertCiliumClusterwideNetworkPolicySpec converts a CiliumClusterwideNetworkPolicy spec to proto.
func convertCiliumClusterwideNetworkPolicySpec(spec map[string]any) *pb.KubernetesCiliumClusterwideNetworkPolicyData {
	policy := &pb.KubernetesCiliumClusterwideNetworkPolicyData{}

	// Extract endpoint selector
	if endpointSelector, found, _ := unstructured.NestedMap(spec, "endpointSelector"); found {
		policy.EndpointSelector = convertCiliumLabelSelector(endpointSelector)
	}

	// Extract node selector (specific to clusterwide policies)
	if nodeSelector, found, _ := unstructured.NestedMap(spec, "nodeSelector"); found {
		policy.NodeSelector = convertCiliumLabelSelector(nodeSelector)
	}

	// Extract ingress rules
	if ingress, found, _ := unstructured.NestedSlice(spec, "ingress"); found {
		policy.IngressRules = convertCiliumRules(ingress)
	}

	// Extract egress rules
	if egress, found, _ := unstructured.NestedSlice(spec, "egress"); found {
		policy.EgressRules = convertCiliumRules(egress)
	}

	// Extract ingress deny rules
	if ingressDeny, found, _ := unstructured.NestedSlice(spec, "ingressDeny"); found {
		policy.IngressDenyRules = convertCiliumRules(ingressDeny)
	}

	// Extract egress deny rules
	if egressDeny, found, _ := unstructured.NestedSlice(spec, "egressDeny"); found {
		policy.EgressDenyRules = convertCiliumRules(egressDeny)
	}

	return policy
}

// convertCiliumLabelSelector converts a Cilium label selector from unstructured to proto.
func convertCiliumLabelSelector(selector map[string]any) *pb.LabelSelector {
	if selector == nil {
		return nil
	}

	result := &pb.LabelSelector{}

	// Extract matchLabels
	if matchLabels, found, _ := unstructured.NestedStringMap(selector, "matchLabels"); found {
		result.MatchLabels = matchLabels
	}

	// Extract matchExpressions
	if matchExpressions, found, _ := unstructured.NestedSlice(selector, "matchExpressions"); found {
		result.MatchExpressions = convertCiliumMatchExpressions(matchExpressions)
	}

	return result
}

// convertCiliumMatchExpressions converts match expressions from unstructured to proto.
func convertCiliumMatchExpressions(expressions []any) []*pb.LabelSelectorRequirement {
	if len(expressions) == 0 {
		return nil
	}

	result := make([]*pb.LabelSelectorRequirement, 0, len(expressions))
	for _, expr := range expressions {
		exprMap, ok := expr.(map[string]any)
		if !ok {
			continue
		}

		req := &pb.LabelSelectorRequirement{}
		if key, found, _ := unstructured.NestedString(exprMap, "key"); found {
			req.Key = key
		}
		if operator, found, _ := unstructured.NestedString(exprMap, "operator"); found {
			req.Operator = operator
		}
		if values, found, _ := unstructured.NestedStringSlice(exprMap, "values"); found {
			req.Values = values
		}

		result = append(result, req)
	}

	return result
}

// convertCiliumRules converts Cilium ingress/egress rules from unstructured to proto.
func convertCiliumRules(rules []any) []*pb.CiliumNetworkPolicyRule {
	if len(rules) == 0 {
		return nil
	}

	result := make([]*pb.CiliumNetworkPolicyRule, 0, len(rules))
	for _, rule := range rules {
		ruleMap, ok := rule.(map[string]any)
		if !ok {
			continue
		}

		protoRule := &pb.CiliumNetworkPolicyRule{}

		// FromEndpoints
		if fromEndpoints, found, _ := unstructured.NestedSlice(ruleMap, "fromEndpoints"); found {
			protoRule.FromEndpoints = convertCiliumEndpointSelectors(fromEndpoints)
		}

		// ToEndpoints
		if toEndpoints, found, _ := unstructured.NestedSlice(ruleMap, "toEndpoints"); found {
			protoRule.ToEndpoints = convertCiliumEndpointSelectors(toEndpoints)
		}

		// FromCIDR
		if fromCIDR, found, _ := unstructured.NestedStringSlice(ruleMap, "fromCIDR"); found {
			protoRule.FromCidr = fromCIDR
		}

		// ToCIDR
		if toCIDR, found, _ := unstructured.NestedStringSlice(ruleMap, "toCIDR"); found {
			protoRule.ToCidr = toCIDR
		}

		// FromCIDRSet
		if fromCIDRSet, found, _ := unstructured.NestedSlice(ruleMap, "fromCIDRSet"); found {
			protoRule.FromCidrSet = convertCiliumCIDRSets(fromCIDRSet)
		}

		// ToCIDRSet
		if toCIDRSet, found, _ := unstructured.NestedSlice(ruleMap, "toCIDRSet"); found {
			protoRule.ToCidrSet = convertCiliumCIDRSets(toCIDRSet)
		}

		// ToPorts
		if toPorts, found, _ := unstructured.NestedSlice(ruleMap, "toPorts"); found {
			protoRule.ToPorts = convertCiliumPortRules(toPorts)
		}

		// FromEntities
		if fromEntities, found, _ := unstructured.NestedStringSlice(ruleMap, "fromEntities"); found {
			protoRule.FromEntities = fromEntities
		}

		// ToEntities
		if toEntities, found, _ := unstructured.NestedStringSlice(ruleMap, "toEntities"); found {
			protoRule.ToEntities = toEntities
		}

		result = append(result, protoRule)
	}

	return result
}

// convertCiliumEndpointSelectors converts endpoint selectors from unstructured to proto.
func convertCiliumEndpointSelectors(selectors []any) []*pb.LabelSelector {
	if len(selectors) == 0 {
		return nil
	}

	result := make([]*pb.LabelSelector, 0, len(selectors))
	for _, selector := range selectors {
		selectorMap, ok := selector.(map[string]any)
		if !ok {
			continue
		}

		result = append(result, convertCiliumLabelSelector(selectorMap))
	}

	return result
}

// convertCiliumCIDRSets converts CIDR sets from unstructured to proto.
func convertCiliumCIDRSets(cidrSets []any) []*pb.CiliumCIDRSet {
	if len(cidrSets) == 0 {
		return nil
	}

	result := make([]*pb.CiliumCIDRSet, 0, len(cidrSets))
	for _, cidrSet := range cidrSets {
		cidrSetMap, ok := cidrSet.(map[string]any)
		if !ok {
			continue
		}

		protoSet := &pb.CiliumCIDRSet{}
		if cidr, found, _ := unstructured.NestedString(cidrSetMap, "cidr"); found {
			protoSet.Cidr = cidr
		}
		if except, found, _ := unstructured.NestedStringSlice(cidrSetMap, "except"); found {
			protoSet.Except = except
		}

		result = append(result, protoSet)
	}

	return result
}

// convertCiliumPortRules converts port rules from unstructured to proto.
func convertCiliumPortRules(portRules []any) []*pb.CiliumPortRule {
	if len(portRules) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPortRule, 0, len(portRules))
	for _, portRule := range portRules {
		portRuleMap, ok := portRule.(map[string]any)
		if !ok {
			continue
		}

		protoRule := &pb.CiliumPortRule{}

		if ports, found, _ := unstructured.NestedSlice(portRuleMap, "ports"); found {
			protoRule.Ports = convertCiliumPorts(ports)
		}

		result = append(result, protoRule)
	}

	return result
}

// convertCiliumPorts converts ports from unstructured to proto.
func convertCiliumPorts(ports []any) []*pb.CiliumPort {
	if len(ports) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPort, 0, len(ports))
	for _, port := range ports {
		portMap, ok := port.(map[string]any)
		if !ok {
			continue
		}

		protoPort := &pb.CiliumPort{}
		if portVal, found, _ := unstructured.NestedString(portMap, "port"); found {
			protoPort.Port = portVal
		}
		if protocol, found, _ := unstructured.NestedString(portMap, "protocol"); found {
			protoPort.Protocol = protocol
		}

		result = append(result, protoPort)
	}

	return result
}
