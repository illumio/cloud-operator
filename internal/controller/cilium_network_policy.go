// Copyright 2024 Illumio, Inc. All Rights Reserved.

// Cilium Network Policy Conversion
//
// Design:
// - Hierarchical structure: Policy -> Specs -> Rules. Each spec has its own
//   endpoint/node selector, enabling policies with multiple target selectors.
// - Reads from both "spec" (single) and "specs" (array) fields, unified into
//   a single specs array in the proto.
// - Ingress/Egress rules are separate types to mirror Cilium's CRD structure
//   and provide compile-time safety (ingress has from_* fields, egress has to_*).
// - Allow/Deny rules share the same type; allow vs deny is determined by which
//   array the rule belongs to (e.g., ingress_rules vs ingress_deny_rules).

package controller

import (
	"fmt"
	"strconv"

	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// IsCiliumPolicy returns true if the input identifies a Cilium network policy.
// Accepts both:
//   - Kind (PascalCase, singular): "CiliumNetworkPolicy", "CiliumClusterwideNetworkPolicy"
//   - Resource name (lowercase, plural): "ciliumnetworkpolicies", "ciliumclusterwidenetworkpolicies"
//
// This dual check exists because callers pass different formats:
//   - Watch events provide Kind from event.Object.GetObjectKind().Kind
//   - Resource listing uses resource name from the resourceList (e.g., "ciliumnetworkpolicies")
//
// TODO: Consider storing Kind in Watcher struct (from K8s Discovery API) to unify on Kind-only checks.
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
		return nil, fmt.Errorf("cannot convert nil object")
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

	// Collect specs from both "spec" (single) and "specs" (array) fields
	apiVersion := obj.GetAPIVersion()
	specs, err := collectCiliumSpecs(obj.Object, apiVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to collect specs for %s/%s: %w", obj.GetNamespace(), obj.GetName(), err)
	}

	switch kind {
	case "CiliumNetworkPolicy":
		ciliumPolicy := &pb.KubernetesCiliumNetworkPolicyData{
			Specs: specs,
		}
		objMetadata.KindSpecific = &pb.KubernetesObjectData_CiliumNetworkPolicy{CiliumNetworkPolicy: ciliumPolicy}
	case "CiliumClusterwideNetworkPolicy":
		ciliumPolicy := &pb.KubernetesCiliumClusterwideNetworkPolicyData{
			Specs: specs,
		}
		objMetadata.KindSpecific = &pb.KubernetesObjectData_CiliumClusterwideNetworkPolicy{CiliumClusterwideNetworkPolicy: ciliumPolicy}
	default:
		return nil, fmt.Errorf("unsupported Cilium policy kind: %s", kind)
	}

	return objMetadata, nil
}

// collectCiliumSpecs extracts specs from both "spec" and "specs" fields.
// Cilium policies can define rules in either a single "spec" or an array of "specs".
func collectCiliumSpecs(obj map[string]any, apiVersion string) ([]*pb.CiliumPolicySpec, error) {
	var result []*pb.CiliumPolicySpec

	// Handle single "spec" field
	spec, found, err := unstructured.NestedMap(obj, "spec")
	if err != nil {
		return nil, fmt.Errorf("invalid spec field type: %w", err)
	}
	if found {
		converted, err := convertCiliumPolicySpec(spec, apiVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to convert spec: %w", err)
		}
		result = append(result, converted)
	}

	// Handle "specs" array field
	specs, found, err := unstructured.NestedSlice(obj, "specs")
	if err != nil {
		return nil, fmt.Errorf("invalid specs field type: %w", err)
	}
	if found {
		for i, spec := range specs {
			specMap, ok := spec.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("specs[%d]: expected map, got %T", i, spec)
			}
			converted, err := convertCiliumPolicySpec(specMap, apiVersion)
			if err != nil {
				return nil, fmt.Errorf("specs[%d]: %w", i, err)
			}
			result = append(result, converted)
		}
	}

	return result, nil
}

// convertCiliumPolicySpec converts a single Cilium spec to proto.
func convertCiliumPolicySpec(spec map[string]any, apiVersion string) (*pb.CiliumPolicySpec, error) {
	policySpec := &pb.CiliumPolicySpec{
		ApiVersion: &apiVersion,
	}

	// Extract endpoint selector
	if endpointSelector, found, _ := unstructured.NestedMap(spec, "endpointSelector"); found {
		policySpec.EndpointSelector = convertCiliumLabelSelector(endpointSelector)
	}

	// Extract node selector (for clusterwide/host policies)
	if nodeSelector, found, _ := unstructured.NestedMap(spec, "nodeSelector"); found {
		policySpec.NodeSelector = convertCiliumLabelSelector(nodeSelector)
	}

	// Extract spec-level metadata
	if description, found, _ := unstructured.NestedString(spec, "description"); found {
		policySpec.Description = &description
	}

	if labels, found, _ := unstructured.NestedStringMap(spec, "labels"); found {
		policySpec.Labels = labels
	}

	// Extract enableDefaultDeny
	if defaultDeny, found, _ := unstructured.NestedMap(spec, "enableDefaultDeny"); found {
		policySpec.EnableDefaultDeny = convertCiliumDefaultDeny(defaultDeny)
	}

	// Extract ingress rules
	if ingress, found, _ := unstructured.NestedSlice(spec, "ingress"); found {
		rules, err := convertCiliumIngressRules(ingress)
		if err != nil {
			return nil, fmt.Errorf("ingress: %w", err)
		}
		policySpec.IngressRules = rules
	}

	// Extract egress rules
	if egress, found, _ := unstructured.NestedSlice(spec, "egress"); found {
		rules, err := convertCiliumEgressRules(egress)
		if err != nil {
			return nil, fmt.Errorf("egress: %w", err)
		}
		policySpec.EgressRules = rules
	}

	// Extract ingress deny rules
	if ingressDeny, found, _ := unstructured.NestedSlice(spec, "ingressDeny"); found {
		rules, err := convertCiliumIngressRules(ingressDeny)
		if err != nil {
			return nil, fmt.Errorf("ingressDeny: %w", err)
		}
		policySpec.IngressDenyRules = rules
	}

	// Extract egress deny rules
	if egressDeny, found, _ := unstructured.NestedSlice(spec, "egressDeny"); found {
		rules, err := convertCiliumEgressRules(egressDeny)
		if err != nil {
			return nil, fmt.Errorf("egressDeny: %w", err)
		}
		policySpec.EgressDenyRules = rules
	}

	return policySpec, nil
}

// convertCiliumDefaultDeny converts enableDefaultDeny config to proto.
func convertCiliumDefaultDeny(defaultDeny map[string]any) *pb.CiliumDefaultDeny {
	result := &pb.CiliumDefaultDeny{}

	if ingress, found, _ := unstructured.NestedBool(defaultDeny, "ingress"); found {
		result.Ingress = &ingress
	}

	if egress, found, _ := unstructured.NestedBool(defaultDeny, "egress"); found {
		result.Egress = &egress
	}

	return result
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

// convertCiliumIngressRules converts Cilium ingress rules from unstructured to proto.
// Note: This function parallels convertCiliumEgressRules intentionally. While ToPorts, ICMPs,
// and Authentication are shared, extracting them would add indirection for minimal benefit.
// The parallel structure mirrors the proto definitions and makes field mapping clear.
func convertCiliumIngressRules(rules []any) ([]*pb.CiliumIngressRule, error) {
	if len(rules) == 0 {
		return nil, nil
	}

	result := make([]*pb.CiliumIngressRule, 0, len(rules))
	for i, rule := range rules {
		ruleMap, ok := rule.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("rule[%d]: expected map, got %T", i, rule)
		}

		protoRule := &pb.CiliumIngressRule{}

		// FromEndpoints
		if fromEndpoints, found, _ := unstructured.NestedSlice(ruleMap, "fromEndpoints"); found {
			protoRule.FromEndpoints = convertCiliumEndpointSelectors(fromEndpoints)
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

		// FromGroups
		if fromGroups, found, _ := unstructured.NestedSlice(ruleMap, "fromGroups"); found {
			protoRule.FromGroups = convertCiliumGroups(fromGroups)
		}

		// FromNodes
		if fromNodes, found, _ := unstructured.NestedSlice(ruleMap, "fromNodes"); found {
			protoRule.FromNodes = convertCiliumEndpointSelectors(fromNodes)
		}

		// ToPorts
		if toPorts, found, _ := unstructured.NestedSlice(ruleMap, "toPorts"); found {
			protoRule.ToPorts = convertCiliumPortRules(toPorts)
		}

		// ICMPs
		if icmps, found, _ := unstructured.NestedSlice(ruleMap, "icmps"); found {
			protoRule.Icmps = convertCiliumICMPRules(icmps)
		}

		// Authentication (not applicable for deny rules, but we capture it if present)
		if auth, found, _ := unstructured.NestedMap(ruleMap, "authentication"); found {
			protoRule.Authentication = convertCiliumAuthentication(auth)
		}

		result = append(result, protoRule)
	}

	return result, nil
}

// convertCiliumEgressRules converts Cilium egress rules from unstructured to proto.
// Note: This function parallels convertCiliumIngressRules intentionally. See comment there.
func convertCiliumEgressRules(rules []any) ([]*pb.CiliumEgressRule, error) {
	if len(rules) == 0 {
		return nil, nil
	}

	result := make([]*pb.CiliumEgressRule, 0, len(rules))
	for i, rule := range rules {
		ruleMap, ok := rule.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("rule[%d]: expected map, got %T", i, rule)
		}

		protoRule := &pb.CiliumEgressRule{}

		// ToEndpoints
		if toEndpoints, found, _ := unstructured.NestedSlice(ruleMap, "toEndpoints"); found {
			protoRule.ToEndpoints = convertCiliumEndpointSelectors(toEndpoints)
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

		// ToFQDNs (not applicable for deny rules, but we capture it if present)
		if toFQDNs, found, _ := unstructured.NestedSlice(ruleMap, "toFQDNs"); found {
			protoRule.ToFqdns = convertCiliumFQDNSelectors(toFQDNs)
		}

		// ToServices
		if toServices, found, _ := unstructured.NestedSlice(ruleMap, "toServices"); found {
			protoRule.ToServices = convertCiliumServices(toServices)
		}

		// ToGroups
		if toGroups, found, _ := unstructured.NestedSlice(ruleMap, "toGroups"); found {
			protoRule.ToGroups = convertCiliumGroups(toGroups)
		}

		// ToNodes
		if toNodes, found, _ := unstructured.NestedSlice(ruleMap, "toNodes"); found {
			protoRule.ToNodes = convertCiliumEndpointSelectors(toNodes)
		}

		// ToPorts
		if toPorts, found, _ := unstructured.NestedSlice(ruleMap, "toPorts"); found {
			protoRule.ToPorts = convertCiliumPortRules(toPorts)
		}

		// ICMPs
		if icmps, found, _ := unstructured.NestedSlice(ruleMap, "icmps"); found {
			protoRule.Icmps = convertCiliumICMPRules(icmps)
		}

		// Authentication (not applicable for deny rules, but we capture it if present)
		if auth, found, _ := unstructured.NestedMap(ruleMap, "authentication"); found {
			protoRule.Authentication = convertCiliumAuthentication(auth)
		}

		result = append(result, protoRule)
	}

	return result, nil
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
		if cidrGroupRef, found, _ := unstructured.NestedString(cidrSetMap, "cidrGroupRef"); found {
			protoSet.CidrGroupRef = &cidrGroupRef
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
		// Port can be a string (named port like "http") or a number (like 80).
		// When unmarshaled to unstructured, numbers become float64.
		if portVal, found, _ := unstructured.NestedString(portMap, "port"); found {
			protoPort.Port = portVal
		} else if portNum, found, _ := unstructured.NestedFloat64(portMap, "port"); found {
			protoPort.Port = strconv.FormatFloat(portNum, 'f', 0, 64)
		}
		if protocol, found, _ := unstructured.NestedString(portMap, "protocol"); found {
			protoPort.Protocol = parseCiliumProtocol(protocol)
		}
		if endPort, found, _ := unstructured.NestedInt64(portMap, "endPort"); found {
			endPortVal := int32(endPort)
			protoPort.EndPort = &endPortVal
		}

		result = append(result, protoPort)
	}

	return result
}

// convertCiliumGroups converts cloud provider security groups from unstructured to proto.
// Cilium's CRD uses cloud provider keys (aws, azure, etc.) as nested objects.
// We use oneof to model this structure, matching Cilium's design.
func convertCiliumGroups(groups []any) []*pb.CiliumGroup {
	if len(groups) == 0 {
		return nil
	}

	result := make([]*pb.CiliumGroup, 0, len(groups))
	for _, group := range groups {
		groupMap, ok := group.(map[string]any)
		if !ok {
			continue
		}

		protoGroup := &pb.CiliumGroup{}

		// AWS cloud provider
		if awsMap, found, _ := unstructured.NestedMap(groupMap, "aws"); found {
			awsGroup := &pb.CiliumAWSGroup{}
			if region, found, _ := unstructured.NestedString(awsMap, "region"); found {
				awsGroup.Region = &region
			}
			// securityGroupsIds is an array of security group IDs in Cilium
			if securityGroupIDs, found, _ := unstructured.NestedStringSlice(awsMap, "securityGroupsIds"); found {
				awsGroup.SecurityGroupIds = securityGroupIDs
			}
			if vpcID, found, _ := unstructured.NestedString(awsMap, "vpcId"); found {
				awsGroup.VpcId = &vpcID
			}
			protoGroup.CloudProvider = &pb.CiliumGroup_Aws{Aws: awsGroup}
		}
		// Future: Add azure, gcp cases here when needed

		result = append(result, protoGroup)
	}

	return result
}

// convertCiliumICMPRules converts ICMP rules from unstructured to proto.
func convertCiliumICMPRules(icmps []any) []*pb.CiliumICMPRule {
	if len(icmps) == 0 {
		return nil
	}

	result := make([]*pb.CiliumICMPRule, 0, len(icmps))
	for _, icmp := range icmps {
		icmpMap, ok := icmp.(map[string]any)
		if !ok {
			continue
		}

		protoICMP := &pb.CiliumICMPRule{}

		if fields, found, _ := unstructured.NestedSlice(icmpMap, "fields"); found {
			protoICMP.Fields = convertCiliumICMPFields(fields)
		}

		result = append(result, protoICMP)
	}

	return result
}

// convertCiliumICMPFields converts ICMP fields from unstructured to proto.
func convertCiliumICMPFields(fields []any) []*pb.CiliumICMPField {
	if len(fields) == 0 {
		return nil
	}

	result := make([]*pb.CiliumICMPField, 0, len(fields))
	for _, field := range fields {
		fieldMap, ok := field.(map[string]any)
		if !ok {
			continue
		}

		protoField := &pb.CiliumICMPField{}

		if icmpType, found, _ := unstructured.NestedInt64(fieldMap, "type"); found {
			protoField.Type = uint32(icmpType)
		}
		if icmpCode, found, _ := unstructured.NestedInt64(fieldMap, "code"); found {
			code := uint32(icmpCode)
			protoField.Code = &code
		}
		if family, found, _ := unstructured.NestedString(fieldMap, "family"); found {
			protoField.Family = &family
		}

		result = append(result, protoField)
	}

	return result
}

// convertCiliumAuthentication converts authentication config from unstructured to proto.
func convertCiliumAuthentication(auth map[string]any) *pb.CiliumAuthentication {
	if auth == nil {
		return nil
	}

	protoAuth := &pb.CiliumAuthentication{}

	if mode, found, _ := unstructured.NestedString(auth, "mode"); found {
		protoAuth.Mode = mode
	}

	return protoAuth
}

// convertCiliumFQDNSelectors converts FQDN selectors from unstructured to proto.
// Note: matchName and matchPattern are mutually exclusive (oneof in proto).
func convertCiliumFQDNSelectors(fqdns []any) []*pb.CiliumFQDNSelector {
	if len(fqdns) == 0 {
		return nil
	}

	result := make([]*pb.CiliumFQDNSelector, 0, len(fqdns))
	for _, fqdn := range fqdns {
		fqdnMap, ok := fqdn.(map[string]any)
		if !ok {
			continue
		}

		protoFQDN := &pb.CiliumFQDNSelector{}

		// matchName and matchPattern are mutually exclusive - check matchName first
		if matchName, found, _ := unstructured.NestedString(fqdnMap, "matchName"); found {
			protoFQDN.Selector = &pb.CiliumFQDNSelector_MatchName{MatchName: matchName}
		} else if matchPattern, found, _ := unstructured.NestedString(fqdnMap, "matchPattern"); found {
			protoFQDN.Selector = &pb.CiliumFQDNSelector_MatchPattern{MatchPattern: matchPattern}
		}

		result = append(result, protoFQDN)
	}

	return result
}

// convertCiliumServices converts service selectors from unstructured to proto.
func convertCiliumServices(services []any) []*pb.CiliumService {
	if len(services) == 0 {
		return nil
	}

	result := make([]*pb.CiliumService, 0, len(services))
	for _, service := range services {
		serviceMap, ok := service.(map[string]any)
		if !ok {
			continue
		}

		protoService := &pb.CiliumService{}

		// K8sService can be specified directly or nested under k8sService
		if k8sService, found, _ := unstructured.NestedMap(serviceMap, "k8sService"); found {
			if serviceName, found, _ := unstructured.NestedString(k8sService, "serviceName"); found {
				protoService.K8SService = &serviceName
			}
			if namespace, found, _ := unstructured.NestedString(k8sService, "namespace"); found {
				protoService.K8SNamespace = &namespace
			}
		}

		// K8sServiceSelector for selecting services by labels.
		// Structure: k8sServiceSelector.selector contains the label selector,
		// k8sServiceSelector.namespace is an optional sibling field.
		if k8sServiceSelector, found, _ := unstructured.NestedMap(serviceMap, "k8sServiceSelector"); found {
			if selector, found, _ := unstructured.NestedMap(k8sServiceSelector, "selector"); found {
				protoService.K8SServiceSelector = convertCiliumLabelSelector(selector)
			}
			if namespace, found, _ := unstructured.NestedString(k8sServiceSelector, "namespace"); found {
				protoService.K8SNamespace = &namespace
			}
		}

		result = append(result, protoService)
	}

	return result
}

// parseCiliumProtocol converts a Cilium protocol string to the CiliumProtocol enum.
func parseCiliumProtocol(protocol string) pb.CiliumProtocol {
	switch protocol {
	case "TCP":
		return pb.CiliumProtocol_CILIUM_PROTOCOL_TCP
	case "UDP":
		return pb.CiliumProtocol_CILIUM_PROTOCOL_UDP
	case "SCTP":
		return pb.CiliumProtocol_CILIUM_PROTOCOL_SCTP
	case "ICMP":
		return pb.CiliumProtocol_CILIUM_PROTOCOL_ICMP
	case "ICMPV6", "ICMPv6":
		return pb.CiliumProtocol_CILIUM_PROTOCOL_ICMPV6
	case "ANY":
		return pb.CiliumProtocol_CILIUM_PROTOCOL_ANY
	default:
		return pb.CiliumProtocol_CILIUM_PROTOCOL_UNSPECIFIED
	}
}
