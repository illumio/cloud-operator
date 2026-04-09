// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// IsCiliumPolicy returns true if the input identifies a Cilium network policy.
// Accepts both Kind (PascalCase) and resource name (lowercase plural).
func IsCiliumPolicy(kindOrResource string) bool {
	switch kindOrResource {
	case "CiliumNetworkPolicy", "CiliumClusterwideNetworkPolicy",
		"ciliumnetworkpolicies", "ciliumclusterwidenetworkpolicies":
		return true
	default:
		return false
	}
}

// ConvertUnstructuredToCiliumPolicy converts an unstructured Cilium policy to a KubernetesObjectData proto.
// This handles both CiliumNetworkPolicy and CiliumClusterwideNetworkPolicy.
func ConvertUnstructuredToCiliumPolicy(obj *unstructured.Unstructured) (*pb.KubernetesObjectData, error) {
	if obj == nil {
		return nil, nil
	}

	kind := obj.GetKind()
	namespace := obj.GetNamespace()

	objMetadata := &pb.KubernetesObjectData{
		Annotations:       obj.GetAnnotations(),
		CreationTimestamp: timestamppb.New(obj.GetCreationTimestamp().Time),
		Kind:              kind,
		Labels:            obj.GetLabels(),
		Name:              obj.GetName(),
		Namespace:         proto.String(namespace),
		OwnerReferences:   convertOwnerReferences(obj.GetOwnerReferences()),
		ResourceVersion:   obj.GetResourceVersion(),
		Uid:               string(obj.GetUID()),
	}

	// Extract specs from both 'spec' (single) and 'specs' (array) fields
	specs := extractCiliumSpecs(obj)

	switch kind {
	case "CiliumNetworkPolicy":
		objMetadata.KindSpecific = &pb.KubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: specs,
			},
		}
	case "CiliumClusterwideNetworkPolicy":
		objMetadata.KindSpecific = &pb.KubernetesObjectData_CiliumClusterwideNetworkPolicy{
			CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
				Specs: specs,
			},
		}
	}

	return objMetadata, nil
}

// extractCiliumSpecs extracts CiliumPolicyRule specs from both 'spec' and 'specs' fields.
func extractCiliumSpecs(obj *unstructured.Unstructured) []*pb.CiliumPolicyRule {
	var result []*pb.CiliumPolicyRule

	// Handle single 'spec' field
	if spec, found, _ := unstructured.NestedMap(obj.Object, "spec"); found {
		if rule := convertCiliumPolicyRule(spec); rule != nil {
			result = append(result, rule)
		}
	}

	// Handle 'specs' array field
	if specs, found, _ := unstructured.NestedSlice(obj.Object, "specs"); found {
		for _, s := range specs {
			if specMap, ok := s.(map[string]any); ok {
				if rule := convertCiliumPolicyRule(specMap); rule != nil {
					result = append(result, rule)
				}
			}
		}
	}

	return result
}

// convertCiliumPolicyRule converts a single Cilium policy spec to proto.
func convertCiliumPolicyRule(spec map[string]any) *pb.CiliumPolicyRule {
	if spec == nil {
		return nil
	}

	rule := &pb.CiliumPolicyRule{}

	// Extract endpoint selector
	if endpointSelector, found, _ := unstructured.NestedMap(spec, "endpointSelector"); found {
		rule.EndpointSelector = convertCiliumLabelSelector(endpointSelector)
	}

	// Extract node selector (for clusterwide policies)
	if nodeSelector, found, _ := unstructured.NestedMap(spec, "nodeSelector"); found {
		rule.NodeSelector = convertCiliumLabelSelector(nodeSelector)
	}

	// Extract description
	if description, found, _ := unstructured.NestedString(spec, "description"); found {
		rule.Description = proto.String(description)
	}

	// Extract labels
	if labels, found, _ := unstructured.NestedStringMap(spec, "labels"); found {
		rule.Labels = labels
	}

	// Extract ingress rules
	if ingress, found, _ := unstructured.NestedSlice(spec, "ingress"); found {
		rule.IngressRules = convertCiliumIngressRules(ingress)
	}

	// Extract egress rules
	if egress, found, _ := unstructured.NestedSlice(spec, "egress"); found {
		rule.EgressRules = convertCiliumEgressRules(egress)
	}

	// Extract ingress deny rules
	if ingressDeny, found, _ := unstructured.NestedSlice(spec, "ingressDeny"); found {
		rule.IngressDenyRules = convertCiliumIngressRules(ingressDeny)
	}

	// Extract egress deny rules
	if egressDeny, found, _ := unstructured.NestedSlice(spec, "egressDeny"); found {
		rule.EgressDenyRules = convertCiliumEgressRules(egressDeny)
	}

	return rule
}

// convertCiliumLabelSelector converts a Cilium label selector from unstructured to proto.
func convertCiliumLabelSelector(selector map[string]any) *pb.LabelSelector {
	if selector == nil {
		return nil
	}

	result := &pb.LabelSelector{}

	if matchLabels, found, _ := unstructured.NestedStringMap(selector, "matchLabels"); found {
		result.MatchLabels = matchLabels
	}

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

// convertCiliumIngressRules converts Cilium ingress rules from unstructured to proto.
func convertCiliumIngressRules(rules []any) []*pb.CiliumPolicyIngressRule {
	if len(rules) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyIngressRule, 0, len(rules))
	for _, rule := range rules {
		ruleMap, ok := rule.(map[string]any)
		if !ok {
			continue
		}

		protoRule := &pb.CiliumPolicyIngressRule{}

		if fromEndpoints, found, _ := unstructured.NestedSlice(ruleMap, "fromEndpoints"); found {
			protoRule.FromEndpoints = &pb.LabelSelectorList{Items: convertCiliumEndpointSelectors(fromEndpoints)}
		}

		if fromCIDR, found, _ := unstructured.NestedStringSlice(ruleMap, "fromCIDR"); found {
			protoRule.FromCidr = fromCIDR
		}

		if fromCIDRSet, found, _ := unstructured.NestedSlice(ruleMap, "fromCIDRSet"); found {
			protoRule.FromCidrSet = convertCiliumCIDRSets(fromCIDRSet)
		}

		if fromEntities, found, _ := unstructured.NestedStringSlice(ruleMap, "fromEntities"); found {
			protoRule.FromEntities = fromEntities
		}

		if toPorts, found, _ := unstructured.NestedSlice(ruleMap, "toPorts"); found {
			protoRule.ToPorts = convertCiliumPortRules(toPorts)
		}

		result = append(result, protoRule)
	}

	return result
}

// convertCiliumEgressRules converts Cilium egress rules from unstructured to proto.
func convertCiliumEgressRules(rules []any) []*pb.CiliumPolicyEgressRule {
	if len(rules) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyEgressRule, 0, len(rules))
	for _, rule := range rules {
		ruleMap, ok := rule.(map[string]any)
		if !ok {
			continue
		}

		protoRule := &pb.CiliumPolicyEgressRule{}

		if toEndpoints, found, _ := unstructured.NestedSlice(ruleMap, "toEndpoints"); found {
			protoRule.ToEndpoints = &pb.LabelSelectorList{Items: convertCiliumEndpointSelectors(toEndpoints)}
		}

		if toCIDR, found, _ := unstructured.NestedStringSlice(ruleMap, "toCIDR"); found {
			protoRule.ToCidr = toCIDR
		}

		if toCIDRSet, found, _ := unstructured.NestedSlice(ruleMap, "toCIDRSet"); found {
			protoRule.ToCidrSet = convertCiliumCIDRSets(toCIDRSet)
		}

		if toEntities, found, _ := unstructured.NestedStringSlice(ruleMap, "toEntities"); found {
			protoRule.ToEntities = toEntities
		}

		if toPorts, found, _ := unstructured.NestedSlice(ruleMap, "toPorts"); found {
			protoRule.ToPorts = convertCiliumPortRules(toPorts)
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
func convertCiliumCIDRSets(cidrSets []any) []*pb.CiliumPolicyCIDRSet {
	if len(cidrSets) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyCIDRSet, 0, len(cidrSets))
	for _, cidrSet := range cidrSets {
		cidrSetMap, ok := cidrSet.(map[string]any)
		if !ok {
			continue
		}

		protoSet := &pb.CiliumPolicyCIDRSet{}
		if cidr, found, _ := unstructured.NestedString(cidrSetMap, "cidr"); found {
			protoSet.Cidr = proto.String(cidr)
		}
		if except, found, _ := unstructured.NestedStringSlice(cidrSetMap, "except"); found {
			protoSet.Except = except
		}

		result = append(result, protoSet)
	}

	return result
}

// convertCiliumPortRules converts port rules from unstructured to proto.
func convertCiliumPortRules(portRules []any) []*pb.CiliumPolicyPortRule {
	if len(portRules) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyPortRule, 0, len(portRules))
	for _, portRule := range portRules {
		portRuleMap, ok := portRule.(map[string]any)
		if !ok {
			continue
		}

		protoRule := &pb.CiliumPolicyPortRule{}

		if ports, found, _ := unstructured.NestedSlice(portRuleMap, "ports"); found {
			protoRule.Ports = convertCiliumPorts(ports)
		}

		result = append(result, protoRule)
	}

	return result
}

// convertCiliumPorts converts ports from unstructured to proto.
func convertCiliumPorts(ports []any) []*pb.CiliumPolicyPort {
	if len(ports) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyPort, 0, len(ports))
	for _, port := range ports {
		portMap, ok := port.(map[string]any)
		if !ok {
			continue
		}

		protoPort := &pb.CiliumPolicyPort{}
		if portVal, found, _ := unstructured.NestedString(portMap, "port"); found {
			protoPort.Port = portVal
		}
		if protocol, found, _ := unstructured.NestedString(portMap, "protocol"); found {
			protoPort.Protocol = proto.String(protocol)
		}

		result = append(result, protoPort)
	}

	return result
}
