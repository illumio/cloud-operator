// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Local copies of AdminNetworkPolicy and BaselineAdminNetworkPolicy CRD types.
//
// We define these locally instead of importing sigs.k8s.io/network-policy-api/apis/v1alpha1
// to avoid pulling in additional transitive dependencies. Since we only read struct fields
// after JSON deserialization and never call any methods, plain structs with matching JSON
// tags are sufficient.
//
// Source: sigs.k8s.io/network-policy-api v0.4.0
//   - apis/v1alpha1/adminnetworkpolicy_types.go
//   - apis/v1alpha1/baseline_adminnetworkpolicy_types.go
//   - apis/v1alpha1/shared_types.go

// adminNetworkPolicy mirrors v1alpha1.AdminNetworkPolicy.
type adminNetworkPolicy struct {
	Spec adminNetworkPolicySpec `json:"spec"`
}

// adminNetworkPolicySpec mirrors v1alpha1.AdminNetworkPolicySpec.
type adminNetworkPolicySpec struct {
	Priority int32                           `json:"priority"`
	Subject  adminNetworkPolicySubject       `json:"subject"`
	Ingress  []adminNetworkPolicyIngressRule `json:"ingress,omitempty"`
	Egress   []adminNetworkPolicyEgressRule  `json:"egress,omitempty"`
}

// baselineAdminNetworkPolicy mirrors v1alpha1.BaselineAdminNetworkPolicy.
type baselineAdminNetworkPolicy struct {
	Spec baselineAdminNetworkPolicySpec `json:"spec"`
}

// baselineAdminNetworkPolicySpec mirrors v1alpha1.BaselineAdminNetworkPolicySpec.
type baselineAdminNetworkPolicySpec struct {
	Subject adminNetworkPolicySubject               `json:"subject"`
	Ingress []baselineAdminNetworkPolicyIngressRule `json:"ingress,omitempty"`
	Egress  []baselineAdminNetworkPolicyEgressRule  `json:"egress,omitempty"`
}

// adminNetworkPolicySubject mirrors v1alpha1.AdminNetworkPolicySubject.
type adminNetworkPolicySubject struct {
	Namespaces *metav1.LabelSelector `json:"namespaces,omitempty"`
	Pods       *namespacedPod        `json:"pods,omitempty"`
}

// namespacedPod mirrors v1alpha1.NamespacedPod.
type namespacedPod struct {
	NamespaceSelector metav1.LabelSelector `json:"namespaceSelector"`
	PodSelector       metav1.LabelSelector `json:"podSelector"`
}

// adminNetworkPolicyIngressRule mirrors v1alpha1.AdminNetworkPolicyIngressRule.
type adminNetworkPolicyIngressRule struct {
	Name   string                          `json:"name,omitempty"`
	Action string                          `json:"action"`
	From   []adminNetworkPolicyIngressPeer `json:"from"`
	Ports  *[]adminNetworkPolicyPort       `json:"ports,omitempty"`
}

// adminNetworkPolicyEgressRule mirrors v1alpha1.AdminNetworkPolicyEgressRule.
type adminNetworkPolicyEgressRule struct {
	Name   string                         `json:"name,omitempty"`
	Action string                         `json:"action"`
	To     []adminNetworkPolicyEgressPeer `json:"to"`
	Ports  *[]adminNetworkPolicyPort      `json:"ports,omitempty"`
}

// baselineAdminNetworkPolicyIngressRule mirrors v1alpha1.BaselineAdminNetworkPolicyIngressRule.
type baselineAdminNetworkPolicyIngressRule struct {
	Name   string                          `json:"name,omitempty"`
	Action string                          `json:"action"`
	From   []adminNetworkPolicyIngressPeer `json:"from"`
	Ports  *[]adminNetworkPolicyPort       `json:"ports,omitempty"`
}

// baselineAdminNetworkPolicyEgressRule mirrors v1alpha1.BaselineAdminNetworkPolicyEgressRule.
type baselineAdminNetworkPolicyEgressRule struct {
	Name   string                         `json:"name,omitempty"`
	Action string                         `json:"action"`
	To     []adminNetworkPolicyEgressPeer `json:"to"`
	Ports  *[]adminNetworkPolicyPort      `json:"ports,omitempty"`
}

// adminNetworkPolicyIngressPeer mirrors v1alpha1.AdminNetworkPolicyIngressPeer.
type adminNetworkPolicyIngressPeer struct {
	Namespaces *metav1.LabelSelector `json:"namespaces,omitempty"`
	Pods       *namespacedPod        `json:"pods,omitempty"`
}

// adminNetworkPolicyEgressPeer mirrors v1alpha1.AdminNetworkPolicyEgressPeer.
type adminNetworkPolicyEgressPeer struct {
	Namespaces  *metav1.LabelSelector `json:"namespaces,omitempty"`
	Pods        *namespacedPod        `json:"pods,omitempty"`
	Nodes       *metav1.LabelSelector `json:"nodes,omitempty"`
	Networks    []string              `json:"networks,omitempty"`
	DomainNames []string              `json:"domainNames,omitempty"`
}

// adminNetworkPolicyPort mirrors v1alpha1.AdminNetworkPolicyPort.
type adminNetworkPolicyPort struct {
	PortNumber *anpPortNumber `json:"portNumber,omitempty"`
	NamedPort  *string        `json:"namedPort,omitempty"`
	PortRange  *anpPortRange  `json:"portRange,omitempty"`
}

// anpPortNumber mirrors v1alpha1.Port.
type anpPortNumber struct {
	Protocol string `json:"protocol"`
	Port     int32  `json:"port"`
}

// anpPortRange mirrors v1alpha1.PortRange.
type anpPortRange struct {
	Protocol string `json:"protocol,omitempty"`
	Start    int32  `json:"start"`
	End      int32  `json:"end"`
}
