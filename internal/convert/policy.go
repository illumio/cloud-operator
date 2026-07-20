// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

const (
	// FieldManager identifies cloud-operator as the owner of fields in Server-Side Apply.
	// It is also used by the watcher to check managedFields ownership on K8s objects.
	FieldManager = "illumio-cloud-operator"
)

// protoJSONMarshaler is configured for Kubernetes Server-Side Apply:
// - UseProtoNames=false: converts snake_case proto fields to camelCase for K8s CRD compatibility
// - EmitUnpopulated=false: omits empty/default fields so SSA doesn't claim ownership of unset fields.
var protoJSONMarshaler = protojson.MarshalOptions{
	UseProtoNames:   false,
	EmitUnpopulated: false,
}

// kindMapping holds the Kubernetes kind and plural resource name for a configured
// object's kind_specific oneof type. It is the single source of truth for the
// kind/resource mapping used by ExtractKind, ExtractResourceName, and spec marshaling.
type kindMapping struct {
	kind         string
	resourceName string
}

// kindMappingFor returns the kind/resource mapping for a configured object's
// kind_specific oneof type, or an error for unsupported types.
func kindMappingFor(data *pb.ConfiguredKubernetesObjectData) (kindMapping, error) {
	switch data.GetKindSpecific().(type) {
	case *pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy:
		return kindMapping{kind: "CiliumNetworkPolicy", resourceName: "ciliumnetworkpolicies"}, nil
	case *pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy:
		return kindMapping{kind: "CiliumClusterwideNetworkPolicy", resourceName: "ciliumclusterwidenetworkpolicies"}, nil
	case *pb.ConfiguredKubernetesObjectData_CiliumCidrGroup:
		return kindMapping{kind: "CiliumCIDRGroup", resourceName: "ciliumcidrgroups"}, nil
	default:
		return kindMapping{}, fmt.Errorf("unsupported kind_specific type: %T", data.GetKindSpecific())
	}
}

// ExtractResourceName returns the plural resource name for the given configured object's kind.
func ExtractResourceName(data *pb.ConfiguredKubernetesObjectData) (string, error) {
	m, err := kindMappingFor(data)
	if err != nil {
		return "", err
	}

	return m.resourceName, nil
}

// ExtractKind returns the Kubernetes kind string for a configured object.
func ExtractKind(data *pb.ConfiguredKubernetesObjectData) (string, error) {
	m, err := kindMappingFor(data)
	if err != nil {
		return "", err
	}

	return m.kind, nil
}

// CacheKey builds a cache key from kind, namespace, and name.
// Both config and runtime caches use this key format so lookups match
// without needing labels or reverse indexes.
func CacheKey(kind, namespace, name string) string {
	return kind + "/" + namespace + "/" + name
}

// CacheKeyForObject computes a cache key from a configured object's kind, namespace, and name.
func CacheKeyForObject(data *pb.ConfiguredKubernetesObjectData) (string, error) {
	kind, err := ExtractKind(data)
	if err != nil {
		return "", err
	}

	return CacheKey(kind, data.GetNamespace(), data.GetName()), nil
}

// BuildConfiguredFromMetadata builds a ConfiguredKubernetesObjectData from the
// already-converted KubernetesObjectData for the runtime cache. Labels and
// annotations are copied as-is so the reconciler can detect drift via proto.Equal.
func BuildConfiguredFromMetadata(metadata *pb.KubernetesObjectData) (*pb.ConfiguredKubernetesObjectData, error) {
	configured := &pb.ConfiguredKubernetesObjectData{
		Name:        metadata.GetName(),
		Namespace:   metadata.Namespace,
		Annotations: metadata.GetAnnotations(),
		Labels:      metadata.GetLabels(),
	}

	if err := setConfiguredKindSpecific(configured, metadata); err != nil {
		return nil, err
	}

	return configured, nil
}

// setConfiguredKindSpecific sets the KindSpecific field on a ConfiguredKubernetesObjectData
// from a KubernetesObjectData source. Both use the same inner types, just different oneof wrappers.
func setConfiguredKindSpecific(configured *pb.ConfiguredKubernetesObjectData, source *pb.KubernetesObjectData) error {
	switch ks := source.GetKindSpecific().(type) {
	case *pb.KubernetesObjectData_CiliumNetworkPolicy:
		configured.KindSpecific = &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{CiliumNetworkPolicy: ks.CiliumNetworkPolicy}
	case *pb.KubernetesObjectData_CiliumClusterwideNetworkPolicy:
		configured.KindSpecific = &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{CiliumClusterwideNetworkPolicy: ks.CiliumClusterwideNetworkPolicy}
	case *pb.KubernetesObjectData_CiliumCidrGroup:
		configured.KindSpecific = &pb.ConfiguredKubernetesObjectData_CiliumCidrGroup{CiliumCidrGroup: ks.CiliumCidrGroup}
	case nil:
		return nil
	default:
		return fmt.Errorf("unhandled KindSpecific type: %T", source.GetKindSpecific())
	}

	return nil
}

// ConvertToApplyObject converts ConfiguredKubernetesObjectData to an *unstructured.Unstructured
// suitable for Server-Side Apply. Proto specs are marshaled via protojson to preserve strict typing
// and produce camelCase field names that match Cilium's CRD schema.
func ConvertToApplyObject(data *pb.ConfiguredKubernetesObjectData, apiGroup, apiVersion string) (*unstructured.Unstructured, string, error) {
	if data == nil {
		return nil, "", errors.New("configured object data is nil")
	}

	fullAPIVersion := apiVersion
	if apiGroup != "" {
		fullAPIVersion = apiGroup + "/" + apiVersion
	}

	// Determine kind, resource name, and marshal specs via protojson
	kind, resourceName, specFields, err := marshalConfiguredObjectSpecs(data)
	if err != nil {
		return nil, "", err
	}

	// Build the K8s object as a map
	metadata := map[string]any{
		"name": data.GetName(),
	}

	// Only include labels/annotations when explicitly set. Omitting the field lets SSA
	// release ownership of previously-owned keys without wiping values from
	// other field managers. Sending null would claim the entire field and clear all keys.
	if labels := data.GetLabels(); labels != nil {
		metadata["labels"] = labels
	}

	if annotations := data.GetAnnotations(); annotations != nil {
		metadata["annotations"] = annotations
	}

	if ns := data.GetNamespace(); ns != "" {
		metadata["namespace"] = ns
	}

	obj := map[string]any{
		"apiVersion": fullAPIVersion,
		"kind":       kind,
		"metadata":   metadata,
	}

	// Merge spec fields (e.g., "spec", "specs") into the top-level object
	maps.Copy(obj, specFields)

	return &unstructured.Unstructured{Object: obj}, resourceName, nil
}

// --- Configured object helpers (reconciler direction: proto → K8s) ---

// marshalConfiguredObjectSpecs returns the K8s kind, plural resource name, and the spec fields as a map,
// using protojson to marshal proto specs into clean JSON that preserves types.
func marshalConfiguredObjectSpecs(data *pb.ConfiguredKubernetesObjectData) (string, string, map[string]any, error) {
	m, err := kindMappingFor(data)
	if err != nil {
		return "", "", nil, err
	}

	var specFields map[string]any

	switch ks := data.GetKindSpecific().(type) {
	case *pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy:
		specFields, err = marshalPolicySpecs(ks.CiliumNetworkPolicy.GetSpecs())

	case *pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy:
		specFields, err = marshalPolicySpecs(ks.CiliumClusterwideNetworkPolicy.GetSpecs())

	case *pb.ConfiguredKubernetesObjectData_CiliumCidrGroup:
		spec := ks.CiliumCidrGroup.GetSpec()
		if spec == nil {
			return m.kind, m.resourceName, map[string]any{}, nil
		}

		var specMap map[string]any

		specMap, err = protoToMap(spec)
		if err == nil {
			specFields = map[string]any{"spec": specMap}
		}
	}

	if err != nil {
		return "", "", nil, fmt.Errorf("failed to marshal %s specs: %w", m.kind, err)
	}

	return m.kind, m.resourceName, specFields, nil
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

		normalizeProtojsonForCilium(specMap)

		return map[string]any{"spec": specMap}, nil
	}

	specsList := make([]any, 0, len(specs))
	for _, spec := range specs {
		specMap, err := protoToMap(spec)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal policy spec: %w", err)
		}

		normalizeProtojsonForCilium(specMap)
		specsList = append(specsList, specMap)
	}

	return map[string]any{"specs": specsList}, nil
}

// normalizeProtojsonForCilium post-processes a policy rule map so that
// protojson output conforms to Cilium's CRD schema:
//   - fromEndpoints/toEndpoints: unwrap LabelSelectorList {"items": [...]} → [...]
//   - ICMP type: rename oneof field "typeInt"/"typeString" → "type"
func normalizeProtojsonForCilium(specMap map[string]any) {
	for _, ruleField := range []string{"ingress", "ingressDeny", "egress", "egressDeny"} {
		rules, ok := specMap[ruleField].([]any)
		if !ok {
			continue
		}

		for _, rule := range rules {
			ruleMap, ok := rule.(map[string]any)
			if !ok {
				continue
			}

			// Unwrap LabelSelectorList wrappers:
			//   {"items": [...]}  → [...]     (normal case)
			//   {} (empty wrapper) → []       (empty LabelSelectorList, items omitted by protojson)
			for _, endpointField := range []string{"fromEndpoints", "toEndpoints"} {
				wrapper, ok := ruleMap[endpointField].(map[string]any)
				if !ok {
					continue
				}

				if items, exists := wrapper["items"]; exists {
					ruleMap[endpointField] = items
				} else {
					ruleMap[endpointField] = []any{}
				}
			}

			// Fix ICMP type oneof fields
			normalizeICMPTypeFields(ruleMap)
		}
	}
}

// normalizeICMPTypeFields renames "typeInt"/"typeString" to "type" in ICMP field entries.
// protojson serializes oneof fields by their variant name, but Cilium expects "type".
func normalizeICMPTypeFields(ruleMap map[string]any) {
	icmps, ok := ruleMap["icmps"].([]any)
	if !ok {
		return
	}

	for _, icmp := range icmps {
		icmpMap, ok := icmp.(map[string]any)
		if !ok {
			continue
		}

		fields, ok := icmpMap["fields"].([]any)
		if !ok {
			continue
		}

		for _, field := range fields {
			fieldMap, ok := field.(map[string]any)
			if !ok {
				continue
			}

			if v, ok := fieldMap["typeInt"]; ok {
				fieldMap["type"] = v
				delete(fieldMap, "typeInt")
			} else if v, ok := fieldMap["typeString"]; ok {
				fieldMap["type"] = v
				delete(fieldMap, "typeString")
			}
		}
	}
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
