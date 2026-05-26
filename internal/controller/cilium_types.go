// Copyright 2026 Illumio, Inc. All Rights Reserved.

package controller

import (
	ciliumPolicy "github.com/cilium/cilium/pkg/policy/api"
)

// Local copies of Cilium CRD top-level types (CiliumNetworkPolicy,
// CiliumClusterwideNetworkPolicy, CiliumCIDRGroup).
//
// We define these locally instead of importing github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2
// because that package transitively pulls in ~47 Cilium-internal packages (REST API clients,
// cloud provider SDKs, eBPF, Prometheus metrics, etc.) adding ~9.6 MB to the binary — none of
// which we use. Since we only read struct fields after JSON deserialization and never call any
// Cilium methods, plain structs with matching JSON tags are sufficient.
//
// We still import ciliumPolicy (pkg/policy/api) for the rule types (Rule, IngressRule, etc.)
// because those have custom UnmarshalJSON methods that are load-bearing for correct
// deserialization. Replacing those would require copying ~30 interconnected types.
//
// We use encoding/json.Unmarshal directly instead of the k8s CodecFactory/Scheme approach
// because every call site already knows the target type (determined by a switch on GVK.Kind),
// so scheme-based type resolution provides no benefit.
//
// Source: github.com/cilium/cilium v1.19.4
//   - pkg/k8s/apis/cilium.io/v2/cnp_types.go
//   - pkg/k8s/apis/cilium.io/v2/ccnp_types.go
//   - pkg/k8s/apis/cilium.io/v2/cidrgroups_types.go

// ciliumNetworkPolicy mirrors ciliumv2.CiliumNetworkPolicy.
type ciliumNetworkPolicy struct {
	Spec  *ciliumPolicy.Rule `json:"spec,omitempty"`
	Specs ciliumPolicy.Rules `json:"specs,omitempty"`
}

// ciliumClusterwideNetworkPolicy mirrors ciliumv2.CiliumClusterwideNetworkPolicy.
type ciliumClusterwideNetworkPolicy struct {
	Spec  *ciliumPolicy.Rule `json:"spec,omitempty"`
	Specs ciliumPolicy.Rules `json:"specs,omitempty"`
}

// ciliumCIDRGroup mirrors ciliumv2.CiliumCIDRGroup.
type ciliumCIDRGroup struct {
	Spec ciliumCIDRGroupSpec `json:"spec"`
}

// ciliumCIDRGroupSpec mirrors ciliumv2.CiliumCIDRGroupSpec.
type ciliumCIDRGroupSpec struct {
	ExternalCIDRs []string `json:"externalCIDRs"`
}
