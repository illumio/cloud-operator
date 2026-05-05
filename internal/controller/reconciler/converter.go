// Copyright 2026 Illumio, Inc. All Rights Reserved.

package reconciler

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// id is the annotation key used to store the CloudSecure object ID.
const id = "cloudsecure-id"

// GetCloudSecureID extracts the CloudSecure ID from an unstructured object's annotations.
// Returns empty string if not found. This should reflect the same ID as in the desired state cache (from ConfiguredObjectCache)
func GetCloudSecureID(obj *unstructured.Unstructured) string {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return ""
	}

	return annotations[id]
}

// ConvertToUnstructured converts a ConfiguredKubernetesObjectData proto to an unstructured Kubernetes object.
// It uses JSON marshaling to convert the proto spec to a map structure that Kubernetes expects.
func ConvertToUnstructured(data *pb.ConfiguredKubernetesObjectData) (*unstructured.Unstructured, error) {
	if data == nil {
		return nil, fmt.Errorf("configured object data is nil")
	}

	obj := &unstructured.Unstructured{
		Object: make(map[string]any),
	}

	// Set metadata
	metadata := map[string]any{
		"name": data.GetName(),
	}

	if ns := data.GetNamespace(); ns != "" {
		metadata["namespace"] = ns
	}

	// Copy annotations from proto and ensure CloudSecure ID is set
	annotations := make(map[string]string)
	for k, v := range data.GetAnnotations() {
		annotations[k] = v
	}
	annotations[id] = data.GetId()
	metadata["annotations"] = annotations

	if labels := data.GetLabels(); len(labels) > 0 {
		metadata["labels"] = labels
	}

	obj.Object["metadata"] = metadata

	// Convert kind-specific data
	switch ks := data.GetKindSpecific().(type) {
	case *pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy:
		obj.SetAPIVersion("cilium.io/v2")
		obj.SetKind("CiliumNetworkPolicy")

		if err := setCiliumPolicySpec(obj, ks.CiliumNetworkPolicy.GetSpecs()); err != nil {
			return nil, fmt.Errorf("failed to set CiliumNetworkPolicy spec: %w", err)
		}

	case *pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy:
		obj.SetAPIVersion("cilium.io/v2")
		obj.SetKind("CiliumClusterwideNetworkPolicy")

		if err := setCiliumPolicySpec(obj, ks.CiliumClusterwideNetworkPolicy.GetSpecs()); err != nil {
			return nil, fmt.Errorf("failed to set CiliumClusterwideNetworkPolicy spec: %w", err)
		}

	case *pb.ConfiguredKubernetesObjectData_CiliumCidrGroup:
		obj.SetAPIVersion("cilium.io/v2alpha1")
		obj.SetKind("CiliumCIDRGroup")

		if err := setCiliumCIDRGroupSpec(obj, ks.CiliumCidrGroup.GetSpec()); err != nil {
			return nil, fmt.Errorf("failed to set CiliumCIDRGroup spec: %w", err)
		}

	default:
		return nil, fmt.Errorf("unsupported kind_specific type: %T", data.GetKindSpecific())
	}

	return obj, nil
}

// setCiliumPolicySpec converts CiliumPolicyRule protos to the unstructured spec/specs fields.
func setCiliumPolicySpec(obj *unstructured.Unstructured, specs []*pb.CiliumPolicyRule) error {
	if len(specs) == 0 {
		return nil
	}

	// Convert each spec using JSON round-trip
	var specMaps []map[string]any
	for _, spec := range specs {
		specMap, err := protoToMap(spec)
		if err != nil {
			return fmt.Errorf("failed to convert spec: %w", err)
		}

		specMaps = append(specMaps, specMap)
	}

	// Cilium supports both `spec` (single) and `specs` (array)
	// Use `spec` for single rule, `specs` for multiple
	if len(specMaps) == 1 {
		obj.Object["spec"] = specMaps[0]
	} else {
		obj.Object["specs"] = specMaps
	}

	return nil
}

// setCiliumCIDRGroupSpec converts a CiliumCIDRGroup proto to the unstructured spec field.
func setCiliumCIDRGroupSpec(obj *unstructured.Unstructured, spec *pb.CiliumCIDRGroup) error {
	if spec == nil {
		return nil
	}

	specMap, err := protoToMap(spec)
	if err != nil {
		return fmt.Errorf("failed to convert spec: %w", err)
	}

	obj.Object["spec"] = specMap

	return nil
}

// protoToMap converts a proto message to a map using JSON marshaling.
// Uses protojson to handle snake_case → camelCase conversion automatically.
func protoToMap(msg any) (map[string]any, error) {
	var jsonBytes []byte
	var err error

	// Use protojson for known proto types to get correct camelCase field names
	switch m := msg.(type) {
	case *pb.CiliumPolicyRule:
		jsonBytes, err = protojson.MarshalOptions{UseProtoNames: false}.Marshal(m)
	case *pb.CiliumCIDRGroup:
		jsonBytes, err = protojson.MarshalOptions{UseProtoNames: false}.Marshal(m)
	default:
		jsonBytes, err = json.Marshal(msg)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to marshal to JSON: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON to map: %w", err)
	}

	return result, nil
}
