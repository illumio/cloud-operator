// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"errors"
	"strconv"

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
		return nil, errors.New("cannot convert nil object")
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

	// Extract enableDefaultDeny
	if enableDefaultDeny, found, _ := unstructured.NestedMap(spec, "enableDefaultDeny"); found {
		rule.EnableDefaultDeny = convertCiliumDefaultDeny(enableDefaultDeny)
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

// convertCiliumDefaultDeny converts enableDefaultDeny from unstructured to proto.
func convertCiliumDefaultDeny(defaultDeny map[string]any) *pb.CiliumPolicyDefaultDeny {
	if defaultDeny == nil {
		return nil
	}

	result := &pb.CiliumPolicyDefaultDeny{}

	if ingress, found, _ := unstructured.NestedBool(defaultDeny, "ingress"); found {
		result.Ingress = proto.Bool(ingress)
	}

	if egress, found, _ := unstructured.NestedBool(defaultDeny, "egress"); found {
		result.Egress = proto.Bool(egress)
	}

	return result
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

		// FromEndpoints
		if fromEndpoints, found, _ := unstructured.NestedSlice(ruleMap, "fromEndpoints"); found {
			protoRule.FromEndpoints = &pb.LabelSelectorList{Items: convertCiliumEndpointSelectors(fromEndpoints)}
		}

		// FromCIDR
		if fromCIDR, found, _ := unstructured.NestedStringSlice(ruleMap, "fromCIDR"); found {
			protoRule.FromCidr = fromCIDR
		}

		// FromCIDRSet
		if fromCIDRSet, found, _ := unstructured.NestedSlice(ruleMap, "fromCIDRSet"); found {
			protoRule.FromCidrSet = convertCiliumCIDRSets(fromCIDRSet)
		}

		// FromEntities
		if fromEntities, found, _ := unstructured.NestedStringSlice(ruleMap, "fromEntities"); found {
			protoRule.FromEntities = fromEntities
		}

		// FromGroups (cloud provider security groups)
		if fromGroups, found, _ := unstructured.NestedSlice(ruleMap, "fromGroups"); found {
			protoRule.FromGroups = convertCiliumGroups(fromGroups)
		}

		// FromNodes (for host policies)
		if fromNodes, found, _ := unstructured.NestedSlice(ruleMap, "fromNodes"); found {
			protoRule.FromNodes = convertCiliumNodeSelectors(fromNodes)
		}

		// ToPorts
		if toPorts, found, _ := unstructured.NestedSlice(ruleMap, "toPorts"); found {
			protoRule.ToPorts = convertCiliumPortRules(toPorts)
		}

		// ICMPs
		if icmps, found, _ := unstructured.NestedSlice(ruleMap, "icmps"); found {
			protoRule.Icmps = convertCiliumICMPRules(icmps)
		}

		// Authentication
		if auth, found, _ := unstructured.NestedMap(ruleMap, "authentication"); found {
			protoRule.Authentication = convertCiliumAuthentication(auth)
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

		// ToEndpoints
		if toEndpoints, found, _ := unstructured.NestedSlice(ruleMap, "toEndpoints"); found {
			protoRule.ToEndpoints = &pb.LabelSelectorList{Items: convertCiliumEndpointSelectors(toEndpoints)}
		}

		// ToCIDR
		if toCIDR, found, _ := unstructured.NestedStringSlice(ruleMap, "toCIDR"); found {
			protoRule.ToCidr = toCIDR
		}

		// ToCIDRSet
		if toCIDRSet, found, _ := unstructured.NestedSlice(ruleMap, "toCIDRSet"); found {
			protoRule.ToCidrSet = convertCiliumCIDRSets(toCIDRSet)
		}

		// ToEntities
		if toEntities, found, _ := unstructured.NestedStringSlice(ruleMap, "toEntities"); found {
			protoRule.ToEntities = toEntities
		}

		// ToFQDNs (DNS-based selectors)
		if toFQDNs, found, _ := unstructured.NestedSlice(ruleMap, "toFQDNs"); found {
			protoRule.ToFqdns = convertCiliumFQDNSelectors(toFQDNs)
		}

		// ToServices (K8s service selectors)
		if toServices, found, _ := unstructured.NestedSlice(ruleMap, "toServices"); found {
			protoRule.ToServices = convertCiliumServices(toServices)
		}

		// ToGroups (cloud provider security groups)
		if toGroups, found, _ := unstructured.NestedSlice(ruleMap, "toGroups"); found {
			protoRule.ToGroups = convertCiliumGroups(toGroups)
		}

		// ToNodes (for host policies)
		if toNodes, found, _ := unstructured.NestedSlice(ruleMap, "toNodes"); found {
			protoRule.ToNodes = convertCiliumNodeSelectors(toNodes)
		}

		// ToPorts
		if toPorts, found, _ := unstructured.NestedSlice(ruleMap, "toPorts"); found {
			protoRule.ToPorts = convertCiliumPortRules(toPorts)
		}

		// ICMPs
		if icmps, found, _ := unstructured.NestedSlice(ruleMap, "icmps"); found {
			protoRule.Icmps = convertCiliumICMPRules(icmps)
		}

		// Authentication
		if auth, found, _ := unstructured.NestedMap(ruleMap, "authentication"); found {
			protoRule.Authentication = convertCiliumAuthentication(auth)
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

// convertCiliumNodeSelectors converts node selectors from unstructured to proto.
func convertCiliumNodeSelectors(selectors []any) []*pb.LabelSelector {
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

		if cidrGroupRef, found, _ := unstructured.NestedString(cidrSetMap, "cidrGroupRef"); found {
			protoSet.CidrGroupRef = proto.String(cidrGroupRef)
		}

		if cidrGroupSelector, found, _ := unstructured.NestedMap(cidrSetMap, "cidrGroupSelector"); found {
			protoSet.CidrGroupSelector = convertCiliumLabelSelector(cidrGroupSelector)
		}

		if except, found, _ := unstructured.NestedStringSlice(cidrSetMap, "except"); found {
			protoSet.Except = except
		}

		result = append(result, protoSet)
	}

	return result
}

// convertCiliumGroups converts cloud provider security groups from unstructured to proto.
func convertCiliumGroups(groups []any) []*pb.CiliumPolicyGroup {
	if len(groups) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyGroup, 0, len(groups))

	for _, group := range groups {
		groupMap, ok := group.(map[string]any)
		if !ok {
			continue
		}

		protoGroup := &pb.CiliumPolicyGroup{}

		// Check for AWS group
		if aws, found, _ := unstructured.NestedMap(groupMap, "aws"); found {
			protoGroup.CloudProvider = &pb.CiliumPolicyGroup_Aws{
				Aws: convertCiliumAWSGroup(aws),
			}
		}

		result = append(result, protoGroup)
	}

	return result
}

// convertCiliumAWSGroup converts AWS security group selector from unstructured to proto.
func convertCiliumAWSGroup(aws map[string]any) *pb.CiliumPolicyAWSGroup {
	if aws == nil {
		return nil
	}

	result := &pb.CiliumPolicyAWSGroup{}

	if labels, found, _ := unstructured.NestedStringMap(aws, "labels"); found {
		result.Labels = labels
	}

	if securityGroupsIds, found, _ := unstructured.NestedStringSlice(aws, "securityGroupsIds"); found {
		result.SecurityGroupIds = securityGroupsIds
	}

	if securityGroupsNames, found, _ := unstructured.NestedStringSlice(aws, "securityGroupsNames"); found {
		result.SecurityGroupNames = securityGroupsNames
	}

	if region, found, _ := unstructured.NestedString(aws, "region"); found {
		result.Region = proto.String(region)
	}

	return result
}

// convertCiliumICMPRules converts ICMP rules from unstructured to proto.
func convertCiliumICMPRules(icmps []any) []*pb.CiliumPolicyICMPRule {
	if len(icmps) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyICMPRule, 0, len(icmps))

	for _, icmp := range icmps {
		icmpMap, ok := icmp.(map[string]any)
		if !ok {
			continue
		}

		protoICMP := &pb.CiliumPolicyICMPRule{}

		if fields, found, _ := unstructured.NestedSlice(icmpMap, "fields"); found {
			protoICMP.Fields = convertCiliumICMPFields(fields)
		}

		result = append(result, protoICMP)
	}

	return result
}

// convertCiliumICMPFields converts ICMP fields from unstructured to proto.
func convertCiliumICMPFields(fields []any) []*pb.CiliumPolicyICMPField {
	if len(fields) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyICMPField, 0, len(fields))

	for _, field := range fields {
		fieldMap, ok := field.(map[string]any)
		if !ok {
			continue
		}

		protoField := &pb.CiliumPolicyICMPField{}

		if family, found, _ := unstructured.NestedString(fieldMap, "family"); found {
			protoField.Family = proto.String(family)
		}

		// Type can be either int or string in Cilium CRD
		// ICMP type values are 0-255 (fits in uint8)
		if typeVal, found, _ := unstructured.NestedFieldNoCopy(fieldMap, "type"); found {
			switch v := typeVal.(type) {
			case int64:
				if v >= 0 && v <= 255 {
					protoField.Type = &pb.CiliumPolicyICMPField_TypeInt{TypeInt: uint32(v)}
				}
			case float64:
				if v >= 0 && v <= 255 {
					protoField.Type = &pb.CiliumPolicyICMPField_TypeInt{TypeInt: uint32(v)}
				}
			case string:
				if intVal, err := strconv.ParseUint(v, 10, 32); err == nil && intVal <= 255 {
					protoField.Type = &pb.CiliumPolicyICMPField_TypeInt{TypeInt: uint32(intVal)}
				}
			}
		}

		result = append(result, protoField)
	}

	return result
}

// convertCiliumAuthentication converts authentication requirements from unstructured to proto.
func convertCiliumAuthentication(auth map[string]any) *pb.CiliumPolicyAuthentication {
	if auth == nil {
		return nil
	}

	result := &pb.CiliumPolicyAuthentication{}

	if mode, found, _ := unstructured.NestedString(auth, "mode"); found {
		result.Mode = mode
	}

	return result
}

// convertCiliumFQDNSelectors converts FQDN selectors from unstructured to proto.
func convertCiliumFQDNSelectors(fqdns []any) []*pb.CiliumPolicyFQDNSelector {
	if len(fqdns) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyFQDNSelector, 0, len(fqdns))

	for _, fqdn := range fqdns {
		fqdnMap, ok := fqdn.(map[string]any)
		if !ok {
			continue
		}

		protoFQDN := &pb.CiliumPolicyFQDNSelector{}

		if matchName, found, _ := unstructured.NestedString(fqdnMap, "matchName"); found {
			protoFQDN.MatchName = proto.String(matchName)
		}

		if matchPattern, found, _ := unstructured.NestedString(fqdnMap, "matchPattern"); found {
			protoFQDN.MatchPattern = proto.String(matchPattern)
		}

		result = append(result, protoFQDN)
	}

	return result
}

// convertCiliumServices converts K8s service selectors from unstructured to proto.
func convertCiliumServices(services []any) []*pb.CiliumPolicyService {
	if len(services) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyService, 0, len(services))

	for _, service := range services {
		serviceMap, ok := service.(map[string]any)
		if !ok {
			continue
		}

		protoService := &pb.CiliumPolicyService{}

		// K8sServiceSelector - select services by label
		if k8sServiceSelector, found, _ := unstructured.NestedMap(serviceMap, "k8sServiceSelector"); found {
			protoService.K8SServiceSelector = convertCiliumK8sServiceSelector(k8sServiceSelector)
		}

		// K8sService - select service by name
		if k8sService, found, _ := unstructured.NestedMap(serviceMap, "k8sService"); found {
			protoService.K8SService = convertCiliumK8sService(k8sService)
		}

		result = append(result, protoService)
	}

	return result
}

// convertCiliumK8sServiceSelector converts K8s service selector from unstructured to proto.
func convertCiliumK8sServiceSelector(selector map[string]any) *pb.CiliumPolicyK8SServiceSelector {
	if selector == nil {
		return nil
	}

	result := &pb.CiliumPolicyK8SServiceSelector{}

	if selectorMap, found, _ := unstructured.NestedMap(selector, "selector"); found {
		result.Selector = convertCiliumLabelSelector(selectorMap)
	}

	if namespace, found, _ := unstructured.NestedString(selector, "namespace"); found {
		result.Namespace = proto.String(namespace)
	}

	return result
}

// convertCiliumK8sService converts K8s service reference from unstructured to proto.
func convertCiliumK8sService(service map[string]any) *pb.CiliumPolicyK8SService {
	if service == nil {
		return nil
	}

	result := &pb.CiliumPolicyK8SService{}

	if serviceName, found, _ := unstructured.NestedString(service, "serviceName"); found {
		result.ServiceName = proto.String(serviceName)
	}

	if namespace, found, _ := unstructured.NestedString(service, "namespace"); found {
		result.Namespace = proto.String(namespace)
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

		// endPort for port ranges (valid port numbers: 0-65535)
		if endPort, found, _ := unstructured.NestedFieldNoCopy(portMap, "endPort"); found {
			switch v := endPort.(type) {
			case int64:
				if v >= 0 && v <= 65535 {
					protoPort.EndPort = proto.Int32(int32(v))
				}
			case float64:
				if v >= 0 && v <= 65535 {
					protoPort.EndPort = proto.Int32(int32(v))
				}
			}
		}

		if protocol, found, _ := unstructured.NestedString(portMap, "protocol"); found {
			protoPort.Protocol = proto.String(protocol)
		}

		result = append(result, protoPort)
	}

	return result
}
