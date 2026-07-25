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
	// CloudSecureIDLabel is the label key used to store the CloudSecure object ID.
	// This ID is the unique key in the desired state (config) cache. It is set as a label on
	// Kubernetes objects during apply so the watcher can extract it and use it as the runtime
	// cache key, allowing the reconciler to match desired vs actual state by the same ID.
	CloudSecureIDLabel = "cloud.illum.io/resource-id"

	// ManagedByLabel is the standard Kubernetes label for identifying the managing component.
	// https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/
	ManagedByLabel = "app.kubernetes.io/managed-by"

	// ManagedByValue is the value used for the managed-by label.
	ManagedByValue = "illumio-cloud-operator"
)

// protoJSONMarshaler is configured for Kubernetes Server-Side Apply:
// - UseProtoNames=false: converts snake_case proto fields to camelCase for K8s CRD compatibility
// - EmitUnpopulated=false: omits empty/default fields so SSA doesn't claim ownership of unset fields.
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
	case *pb.ConfiguredKubernetesObjectData_AwsClusterNetworkPolicy:
		return "clusternetworkpolicies", nil
	default:
		return "", fmt.Errorf("unsupported kind_specific type: %T", data.GetKindSpecific())
	}
}

// BuildConfiguredFromMetadata builds a ConfiguredKubernetesObjectData from the
// already-converted KubernetesObjectData for the runtime cache. Operator-added
// labels (cloudsecure-id, managed-by) are stripped so the runtime snapshot
// matches the shape of the config cache. Annotations are passed through as-is
// because the operator doesn't add any — SSA handles annotation ownership.
func BuildConfiguredFromMetadata(id string, metadata *pb.KubernetesObjectData) (*pb.ConfiguredKubernetesObjectData, error) {
	filteredLabels := make(map[string]string, len(metadata.GetLabels()))
	for k, v := range metadata.GetLabels() {
		if k != CloudSecureIDLabel && k != ManagedByLabel {
			filteredLabels[k] = v
		}
	}

	configured := &pb.ConfiguredKubernetesObjectData{
		Id:          id,
		Name:        metadata.GetName(),
		Namespace:   metadata.Namespace,
		Annotations: metadata.GetAnnotations(),
		Labels:      filteredLabels,
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
	case *pb.KubernetesObjectData_AwsClusterNetworkPolicy:
		configured.KindSpecific = &pb.ConfiguredKubernetesObjectData_AwsClusterNetworkPolicy{AwsClusterNetworkPolicy: ks.AwsClusterNetworkPolicy}
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
		"name":   data.GetName(),
		"labels": copyLabels(data.GetLabels(), data.GetId()),
	}

	// Only include annotations when explicitly set. Omitting the field lets SSA
	// release ownership of previously-owned keys without wiping annotations from
	// other field managers. Sending null would claim the entire field and clear all keys.
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

// copyLabels copies labels and adds the CloudSecure ID and managed-by labels.
func copyLabels(labels map[string]string, id string) map[string]string {
	result := make(map[string]string, len(labels)+2)
	maps.Copy(result, labels)

	result[CloudSecureIDLabel] = id
	result[ManagedByLabel] = ManagedByValue

	return result
}

// --- Configured object helpers (reconciler direction: proto → K8s) ---

// marshalConfiguredObjectSpecs returns the K8s kind, plural resource name, and the spec fields as a map,
// using protojson to marshal proto specs into clean JSON that preserves types.
func marshalConfiguredObjectSpecs(data *pb.ConfiguredKubernetesObjectData) (string, string, map[string]any, error) {
	resourceName, err := ExtractResourceName(data)
	if err != nil {
		return "", "", nil, err
	}

	var (
		kind       string
		specFields map[string]any
	)

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

	case *pb.ConfiguredKubernetesObjectData_AwsClusterNetworkPolicy:
		kind = "ClusterNetworkPolicy"

		var specMap map[string]any

		specMap, err = protoToMap(ks.AwsClusterNetworkPolicy)
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
