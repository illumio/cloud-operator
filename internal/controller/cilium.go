// Copyright 2026 Illumio, Inc. All Rights Reserved.

package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"

	ciliumSlimMetav1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/apis/meta/v1"
	ciliumLabels "github.com/cilium/cilium/pkg/labels"
	ciliumPolicy "github.com/cilium/cilium/pkg/policy/api"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	k8sUnstructured "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sIntstr "k8s.io/apimachinery/pkg/util/intstr"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// protoJSONMarshaler is configured for Kubernetes Server-Side Apply:
// - UseProtoNames=false: converts snake_case proto fields to camelCase for K8s CRD compatibility
// - EmitUnpopulated=false: omits empty/default fields so SSA doesn't claim ownership of unset fields
var protoJSONMarshaler = protojson.MarshalOptions{
	UseProtoNames:   false,
	EmitUnpopulated: false,
}

// ExtractResourceName returns the plural resource name for the given configured object's kind.
func ExtractResourceName(data *pb.ConfiguredKubernetesObjectData) (string, error) {
	switch data.GetKindSpecific().(type) {
	case *pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy:
		return "ciliumnetworkpolicies", nil
	case *pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy:
		return "ciliumclusterwidenetworkpolicies", nil
	case *pb.ConfiguredKubernetesObjectData_CiliumCidrGroup:
		return "ciliumcidrgroups", nil
	default:
		return "", fmt.Errorf("unsupported kind_specific type: %T", data.GetKindSpecific())
	}
}

// IsCiliumResource returns true if the input identifies a Cilium resource.
// Accepts both Kind (PascalCase) and resource name (lowercase plural).
func IsCiliumResource(kindOrResource string) bool {
	switch kindOrResource {
	case "CiliumNetworkPolicy", "CiliumClusterwideNetworkPolicy", "CiliumCIDRGroup",
		"ciliumnetworkpolicies", "ciliumclusterwidenetworkpolicies", "ciliumcidrgroups":
		return true
	default:
		return false
	}
}

// ConvertUnstructuredToCiliumResource converts an unstructured Cilium resource to a KubernetesObjectData proto.
// This handles CiliumNetworkPolicy, CiliumClusterwideNetworkPolicy, and CiliumCIDRGroup.
func ConvertUnstructuredToCiliumResource(obj *k8sUnstructured.Unstructured) (*pb.KubernetesObjectData, error) {
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
	case "CiliumNetworkPolicy":
		var cnp ciliumNetworkPolicy
		if err := json.Unmarshal(jsonBytes, &cnp); err != nil {
			return nil, fmt.Errorf("deserializing %s: %w", gvk.Kind, err)
		}

		objMetadata.KindSpecific = &pb.KubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: convertCiliumSpecs(cnp.Spec, cnp.Specs),
			},
		}
	case "CiliumClusterwideNetworkPolicy":
		var ccnp ciliumClusterwideNetworkPolicy
		if err := json.Unmarshal(jsonBytes, &ccnp); err != nil {
			return nil, fmt.Errorf("deserializing %s: %w", gvk.Kind, err)
		}

		objMetadata.KindSpecific = &pb.KubernetesObjectData_CiliumClusterwideNetworkPolicy{
			CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
				Specs: convertCiliumSpecs(ccnp.Spec, ccnp.Specs),
			},
		}
	case "CiliumCIDRGroup":
		var ccg ciliumCIDRGroup
		if err := json.Unmarshal(jsonBytes, &ccg); err != nil {
			return nil, fmt.Errorf("deserializing %s: %w", gvk.Kind, err)
		}

		objMetadata.KindSpecific = &pb.KubernetesObjectData_CiliumCidrGroup{
			CiliumCidrGroup: convertCiliumCIDRGroupData(&ccg),
		}
	default:
		return nil, fmt.Errorf("unsupported Cilium resource kind: %s", gvk.Kind)
	}

	return objMetadata, nil
}

func convertCiliumSpecs(spec *ciliumPolicy.Rule, specs ciliumPolicy.Rules) []*pb.CiliumPolicyRule {
	var result []*pb.CiliumPolicyRule

	if spec != nil {
		result = append(result, convertCiliumPolicyRule(spec))
	}

	for _, s := range specs {
		if s != nil {
			result = append(result, convertCiliumPolicyRule(s))
		}
	}

	return result
}

func convertCiliumPolicyRule(rule *ciliumPolicy.Rule) *pb.CiliumPolicyRule {
	pbRule := &pb.CiliumPolicyRule{
		EndpointSelector:  convertEndpointSelector(rule.EndpointSelector),
		NodeSelector:      convertEndpointSelector(rule.NodeSelector),
		Labels:            convertLabelArray(rule.Labels),
		EnableDefaultDeny: convertDefaultDeny(rule.EnableDefaultDeny),
	}

	if rule.Description != "" {
		pbRule.Description = &rule.Description
	}

	if len(rule.Ingress) > 0 {
		pbRule.Ingress = convertIngressRules(rule.Ingress)
	}

	if len(rule.Egress) > 0 {
		pbRule.Egress = convertEgressRules(rule.Egress)
	}

	if len(rule.IngressDeny) > 0 {
		pbRule.IngressDeny = convertIngressDenyRules(rule.IngressDeny)
	}

	if len(rule.EgressDeny) > 0 {
		pbRule.EgressDeny = convertEgressDenyRules(rule.EgressDeny)
	}

	return pbRule
}

// --- Label selector conversion (handles Cilium slim types) ---

func convertEndpointSelector(sel ciliumPolicy.EndpointSelector) *pb.LabelSelector {
	return convertSlimLabelSelector(sel.LabelSelector)
}

func convertSlimLabelSelector(sel *ciliumSlimMetav1.LabelSelector) *pb.LabelSelector {
	if sel == nil {
		return nil
	}

	pbSel := &pb.LabelSelector{}

	if len(sel.MatchLabels) > 0 {
		matchLabels := make(map[string]string, len(sel.MatchLabels))
		maps.Copy(matchLabels, sel.MatchLabels)
		pbSel.MatchLabels = matchLabels
	}

	if len(sel.MatchExpressions) > 0 {
		pbSel.MatchExpressions = make([]*pb.LabelSelectorRequirement, 0, len(sel.MatchExpressions))
		for _, expr := range sel.MatchExpressions {
			pbSel.MatchExpressions = append(pbSel.MatchExpressions, &pb.LabelSelectorRequirement{
				Key:      expr.Key,
				Operator: string(expr.Operator),
				Values:   expr.Values,
			})
		}
	}

	return pbSel
}

func convertEndpointSelectors(sels []ciliumPolicy.EndpointSelector) []*pb.LabelSelector {
	if len(sels) == 0 {
		return nil
	}

	result := make([]*pb.LabelSelector, 0, len(sels))
	for _, sel := range sels {
		result = append(result, convertEndpointSelector(sel))
	}

	return result
}

// --- Ingress / Egress rules ---

func convertIngressRules(rules []ciliumPolicy.IngressRule) []*pb.CiliumPolicyIngressRule {
	result := make([]*pb.CiliumPolicyIngressRule, 0, len(rules))
	for _, rule := range rules {
		pbRule := convertIngressCommonFields(rule.IngressCommonRule)
		pbRule.ToPorts = convertPortRules(rule.ToPorts)
		pbRule.Icmps = convertICMPRules(rule.ICMPs)
		pbRule.Authentication = convertAuthentication(rule.Authentication)
		result = append(result, pbRule)
	}

	return result
}

func convertIngressDenyRules(rules []ciliumPolicy.IngressDenyRule) []*pb.CiliumPolicyIngressRule {
	result := make([]*pb.CiliumPolicyIngressRule, 0, len(rules))
	for _, rule := range rules {
		pbRule := convertIngressCommonFields(rule.IngressCommonRule)
		pbRule.ToPorts = convertPortDenyRules(rule.ToPorts)
		pbRule.Icmps = convertICMPRules(rule.ICMPs)
		result = append(result, pbRule)
	}

	return result
}

func convertIngressCommonFields(common ciliumPolicy.IngressCommonRule) *pb.CiliumPolicyIngressRule {
	pbRule := &pb.CiliumPolicyIngressRule{}

	if len(common.FromEndpoints) > 0 {
		pbRule.FromEndpoints = &pb.LabelSelectorList{Items: convertEndpointSelectors(common.FromEndpoints)}
	}

	if len(common.FromCIDR) > 0 {
		pbRule.FromCidr = cidrSliceToStrings(common.FromCIDR)
	}

	if len(common.FromCIDRSet) > 0 {
		pbRule.FromCidrSet = convertCIDRRules(common.FromCIDRSet)
	}

	if len(common.FromEntities) > 0 {
		pbRule.FromEntities = entitySliceToStrings(common.FromEntities)
	}

	if len(common.FromGroups) > 0 {
		pbRule.FromGroups = convertGroups(common.FromGroups)
	}

	if len(common.FromNodes) > 0 {
		pbRule.FromNodes = convertEndpointSelectors(common.FromNodes)
	}

	return pbRule
}

func convertEgressRules(rules []ciliumPolicy.EgressRule) []*pb.CiliumPolicyEgressRule {
	result := make([]*pb.CiliumPolicyEgressRule, 0, len(rules))
	for _, rule := range rules {
		pbRule := convertEgressCommonFields(rule.EgressCommonRule)
		pbRule.ToPorts = convertPortRules(rule.ToPorts)
		pbRule.ToFqdns = convertFQDNSelectors(rule.ToFQDNs)
		pbRule.Icmps = convertICMPRules(rule.ICMPs)
		pbRule.Authentication = convertAuthentication(rule.Authentication)
		result = append(result, pbRule)
	}

	return result
}

func convertEgressDenyRules(rules []ciliumPolicy.EgressDenyRule) []*pb.CiliumPolicyEgressRule {
	result := make([]*pb.CiliumPolicyEgressRule, 0, len(rules))
	for _, rule := range rules {
		pbRule := convertEgressCommonFields(rule.EgressCommonRule)
		pbRule.ToPorts = convertPortDenyRules(rule.ToPorts)
		pbRule.Icmps = convertICMPRules(rule.ICMPs)
		result = append(result, pbRule)
	}

	return result
}

func convertEgressCommonFields(common ciliumPolicy.EgressCommonRule) *pb.CiliumPolicyEgressRule {
	pbRule := &pb.CiliumPolicyEgressRule{}

	if len(common.ToEndpoints) > 0 {
		pbRule.ToEndpoints = &pb.LabelSelectorList{Items: convertEndpointSelectors(common.ToEndpoints)}
	}

	if len(common.ToCIDR) > 0 {
		pbRule.ToCidr = cidrSliceToStrings(common.ToCIDR)
	}

	if len(common.ToCIDRSet) > 0 {
		pbRule.ToCidrSet = convertCIDRRules(common.ToCIDRSet)
	}

	if len(common.ToEntities) > 0 {
		pbRule.ToEntities = entitySliceToStrings(common.ToEntities)
	}

	if len(common.ToServices) > 0 {
		pbRule.ToServices = convertServices(common.ToServices)
	}

	if len(common.ToGroups) > 0 {
		pbRule.ToGroups = convertGroups(common.ToGroups)
	}

	if len(common.ToNodes) > 0 {
		pbRule.ToNodes = convertEndpointSelectors(common.ToNodes)
	}

	return pbRule
}

// --- Port rules ---

// L7 fields (Rules, TerminatingTLS, OriginatingTLS, ServerNames, Listener) are intentionally
// not converted — our proto only models L3/L4 policy data.
func convertPortRules(rules ciliumPolicy.PortRules) []*pb.CiliumPolicyPortRule {
	if len(rules) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyPortRule, 0, len(rules))
	for _, rule := range rules {
		result = append(result, &pb.CiliumPolicyPortRule{
			Ports: convertPortProtocols(rule.Ports),
		})
	}

	return result
}

func convertPortDenyRules(rules ciliumPolicy.PortDenyRules) []*pb.CiliumPolicyPortRule {
	if len(rules) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyPortRule, 0, len(rules))
	for _, rule := range rules {
		result = append(result, &pb.CiliumPolicyPortRule{
			Ports: convertPortProtocols(rule.Ports),
		})
	}

	return result
}

func convertPortProtocols(ports []ciliumPolicy.PortProtocol) []*pb.CiliumPolicyPort {
	if len(ports) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyPort, 0, len(ports))
	for _, p := range ports {
		pbPort := &pb.CiliumPolicyPort{
			Port: p.Port,
		}

		if p.EndPort != 0 {
			ep := p.EndPort
			pbPort.EndPort = &ep
		}

		if p.Protocol != "" {
			proto := string(p.Protocol)
			pbPort.Protocol = &proto
		}

		result = append(result, pbPort)
	}

	return result
}

// --- CIDR rules ---

func convertCIDRRules(rules ciliumPolicy.CIDRRuleSlice) []*pb.CiliumPolicyCIDRSet {
	if len(rules) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyCIDRSet, 0, len(rules))
	for _, rule := range rules {
		pbSet := &pb.CiliumPolicyCIDRSet{}

		if rule.Cidr != "" {
			cidr := string(rule.Cidr)
			pbSet.Cidr = &cidr
		}

		if rule.CIDRGroupRef != "" {
			ref := string(rule.CIDRGroupRef)
			pbSet.CidrGroupRef = &ref
		}

		if rule.CIDRGroupSelector.LabelSelector != nil {
			pbSet.CidrGroupSelector = convertEndpointSelector(rule.CIDRGroupSelector)
		}

		if len(rule.ExceptCIDRs) > 0 {
			pbSet.Except = cidrSliceToStrings(ciliumPolicy.CIDRSlice(rule.ExceptCIDRs))
		}

		result = append(result, pbSet)
	}

	return result
}

// --- ICMP rules ---

func convertICMPRules(rules ciliumPolicy.ICMPRules) []*pb.CiliumPolicyICMPRule {
	if len(rules) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyICMPRule, 0, len(rules))
	for _, rule := range rules {
		result = append(result, &pb.CiliumPolicyICMPRule{
			Fields: convertICMPFields(rule.Fields),
		})
	}

	return result
}

func convertICMPFields(fields []ciliumPolicy.ICMPField) []*pb.CiliumPolicyICMPField {
	if len(fields) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyICMPField, 0, len(fields))
	for _, f := range fields {
		pbField := &pb.CiliumPolicyICMPField{}

		if f.Family != "" {
			pbField.Family = &f.Family
		}

		if f.Type != nil {
			switch f.Type.Type {
			case k8sIntstr.Int:
				v := f.Type.IntValue()
				if v >= 0 && v <= 255 {
					pbField.Type = &pb.CiliumPolicyICMPField_TypeInt{TypeInt: uint32(v)}
				}
			case k8sIntstr.String:
				s := f.Type.StrVal
				if intVal, err := strconv.ParseUint(s, 10, 32); err == nil && intVal <= 255 {
					pbField.Type = &pb.CiliumPolicyICMPField_TypeInt{TypeInt: uint32(intVal)}
				} else if _, err := strconv.ParseInt(s, 10, 64); err == nil {
					// Negative numeric string (e.g. "-1"): drop it, consistent with
					// the integer case where negative values are silently ignored.
				} else {
					pbField.Type = &pb.CiliumPolicyICMPField_TypeString{TypeString: s}
				}
			}
		}

		result = append(result, pbField)
	}

	return result
}

// --- FQDN selectors ---

func convertFQDNSelectors(fqdns ciliumPolicy.FQDNSelectorSlice) []*pb.CiliumPolicyFQDNSelector {
	if len(fqdns) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyFQDNSelector, 0, len(fqdns))
	for _, f := range fqdns {
		pbFQDN := &pb.CiliumPolicyFQDNSelector{}

		if f.MatchName != "" {
			pbFQDN.MatchName = &f.MatchName
		}

		if f.MatchPattern != "" {
			pbFQDN.MatchPattern = &f.MatchPattern
		}

		result = append(result, pbFQDN)
	}

	return result
}

// --- Service selectors ---

func convertServices(services []ciliumPolicy.Service) []*pb.CiliumPolicyService {
	if len(services) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyService, 0, len(services))
	for _, svc := range services {
		pbSvc := &pb.CiliumPolicyService{}

		if svc.K8sServiceSelector != nil {
			pbSvc.K8SServiceSelector = &pb.CiliumPolicyK8SServiceSelector{
				Selector: convertSlimLabelSelector(svc.K8sServiceSelector.Selector.LabelSelector),
			}
			if svc.K8sServiceSelector.Namespace != "" {
				pbSvc.K8SServiceSelector.Namespace = &svc.K8sServiceSelector.Namespace
			}
		}

		if svc.K8sService != nil {
			pbSvc.K8SService = &pb.CiliumPolicyK8SService{}

			if svc.K8sService.ServiceName != "" {
				pbSvc.K8SService.ServiceName = &svc.K8sService.ServiceName
			}

			if svc.K8sService.Namespace != "" {
				pbSvc.K8SService.Namespace = &svc.K8sService.Namespace
			}
		}

		result = append(result, pbSvc)
	}

	return result
}

// --- Cloud provider groups ---

func convertGroups(groups []ciliumPolicy.Groups) []*pb.CiliumPolicyGroup {
	if len(groups) == 0 {
		return nil
	}

	result := make([]*pb.CiliumPolicyGroup, 0, len(groups))
	for _, g := range groups {
		if g.AWS == nil {
			continue
		}

		awsGroup := &pb.CiliumPolicyAWSGroup{
			Labels:             g.AWS.Labels,
			SecurityGroupIds:   g.AWS.SecurityGroupsIds,
			SecurityGroupNames: g.AWS.SecurityGroupsNames,
		}

		if g.AWS.Region != "" {
			awsGroup.Region = &g.AWS.Region
		}

		result = append(result, &pb.CiliumPolicyGroup{
			CloudProvider: &pb.CiliumPolicyGroup_Aws{Aws: awsGroup},
		})
	}

	return result
}

// --- Authentication ---

func convertAuthentication(auth *ciliumPolicy.Authentication) *pb.CiliumPolicyAuthentication {
	if auth == nil {
		return nil
	}

	return &pb.CiliumPolicyAuthentication{
		Mode: string(auth.Mode),
	}
}

// --- Default deny ---

func convertDefaultDeny(config ciliumPolicy.DefaultDenyConfig) *pb.CiliumPolicyDefaultDeny {
	if config.Ingress == nil && config.Egress == nil {
		return nil
	}

	return &pb.CiliumPolicyDefaultDeny{
		Ingress: config.Ingress,
		Egress:  config.Egress,
	}
}

// --- Label array ---

func convertLabelArray(arr ciliumLabels.LabelArray) map[string]string {
	if len(arr) == 0 {
		return nil
	}

	result := make(map[string]string, len(arr))
	for _, l := range arr {
		if l.Key != "" {
			result[l.Key] = l.Value
		}
	}

	return result
}

// --- Type conversion helpers ---

func cidrSliceToStrings(cidrs ciliumPolicy.CIDRSlice) []string {
	result := make([]string, len(cidrs))
	for i, c := range cidrs {
		result[i] = string(c)
	}

	return result
}

func entitySliceToStrings(entities ciliumPolicy.EntitySlice) []string {
	result := make([]string, len(entities))
	for i, e := range entities {
		result[i] = string(e)
	}

	return result
}

// --- CIDRGroup conversion ---

func convertCiliumCIDRGroupData(ccg *ciliumCIDRGroup) *pb.KubernetesCiliumCIDRGroupData {
	return &pb.KubernetesCiliumCIDRGroupData{
		Spec: &pb.CiliumCIDRGroup{
			ExternalCidrs: ccg.Spec.ExternalCIDRs,
		},
	}
}

// --- Configured object helpers (reconciler direction: proto → K8s) ---

// marshalConfiguredObjectSpecs returns the K8s kind, plural resource name, and the spec fields as a map,
// using protojson to marshal proto specs into clean JSON that preserves types.
func marshalConfiguredObjectSpecs(data *pb.ConfiguredKubernetesObjectData) (string, string, map[string]any, error) {
	resourceName, err := ExtractResourceName(data)
	if err != nil {
		return "", "", nil, err
	}

	var kind string
	var specFields map[string]any

	switch ks := data.GetKindSpecific().(type) {
	case *pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy:
		kind = "CiliumNetworkPolicy"
		specFields, err = marshalPolicySpecs(ks.CiliumNetworkPolicy.GetSpecs())

	case *pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy:
		kind = "CiliumClusterwideNetworkPolicy"
		specFields, err = marshalPolicySpecs(ks.CiliumClusterwideNetworkPolicy.GetSpecs())

	case *pb.ConfiguredKubernetesObjectData_CiliumCidrGroup:
		kind = "CiliumCIDRGroup"
		spec := ks.CiliumCidrGroup.GetSpec()
		if spec == nil {
			return kind, resourceName, map[string]any{}, nil
		}

		var specMap map[string]any
		specMap, err = protoToMap(spec)
		if err == nil {
			specFields = map[string]any{"spec": specMap}
		}

	default:
		return "", "", nil, fmt.Errorf("unsupported kind_specific type: %T", data.GetKindSpecific())
	}

	if err != nil {
		return "", "", nil, fmt.Errorf("failed to marshal %s specs: %w", kind, err)
	}

	return kind, resourceName, specFields, nil
}

// marshalPolicySpecs marshals Cilium policy rules into the "spec" or "specs" field.
// Cilium CRDs accept either a single "spec" or an array "specs".
func marshalPolicySpecs(specs []*pb.CiliumPolicyRule) (map[string]any, error) {
	if len(specs) == 0 {
		return map[string]any{}, nil
	}

	if len(specs) == 1 {
		specMap, err := protoToMap(specs[0])
		if err != nil {
			return nil, fmt.Errorf("failed to marshal policy spec: %w", err)
		}

		return map[string]any{"spec": specMap}, nil
	}

	specsList := make([]any, 0, len(specs))
	for _, spec := range specs {
		specMap, err := protoToMap(spec)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal policy spec: %w", err)
		}

		specsList = append(specsList, specMap)
	}

	return map[string]any{"specs": specsList}, nil
}

// protoToMap marshals a proto message to JSON via protojson, then unmarshals
// into a map[string]any. This preserves strict typing (integers, booleans)
// and produces camelCase field names matching Cilium's CRD schema.
func protoToMap(msg proto.Message) (map[string]any, error) {
	jsonBytes, err := protoJSONMarshaler.Marshal(msg)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		return nil, err
	}

	return result, nil
}
