// Copyright 2026 Illumio, Inc. All Rights Reserved.

package ovn

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Local copies of the OpenShift OVN EgressFirewall and EgressIP CRD types.
//
// We define these locally instead of importing the OpenShift API modules to avoid pulling in
// additional transitive dependencies. Since we only read struct fields after JSON deserialization
// and never call any methods, plain structs with matching JSON tags are sufficient.
//
// Sources:
//   - github.com/openshift/api/network/v1: egressfirewall_types.go
//   - github.com/openshift/api/operator/v1: types_egressip.go (k8s.ovn.org EgressIP)

// egressFirewall mirrors the OVN EgressFirewall CRD (k8s.ovn.org).
type egressFirewall struct {
	Spec egressFirewallSpec `json:"spec"`
}

// egressFirewallSpec mirrors EgressFirewallSpec.
type egressFirewallSpec struct {
	// Egress is the ordered list of egress firewall rule objects.
	Egress []egressFirewallRule `json:"egress"`
}

// egressFirewallRule mirrors EgressFirewallRule.
type egressFirewallRule struct {
	// Type marks the rule as either "Allow" or "Deny".
	Type string `json:"type"`
	// To is the target the rule applies to.
	To egressFirewallDestination `json:"to"`
	// Ports is an optional list of ports the rule applies to.
	Ports []egressFirewallPort `json:"ports,omitempty"`
}

// egressFirewallDestination mirrors EgressFirewallDestination.
// Exactly one of CIDRSelector or DNSName is set.
type egressFirewallDestination struct {
	CIDRSelector string `json:"cidrSelector,omitempty"`
	DNSName      string `json:"dnsName,omitempty"`
}

// egressFirewallPort mirrors EgressFirewallPort.
type egressFirewallPort struct {
	Protocol string `json:"protocol"`
	Port     int32  `json:"port"`
}

// egressIP mirrors the OVN EgressIP CRD (k8s.ovn.org).
type egressIP struct {
	Spec egressIPSpec `json:"spec"`
}

// egressIPSpec mirrors EgressIPSpec.
type egressIPSpec struct {
	// EgressIPs is the list of egress IP addresses requested for the selected pods.
	EgressIPs []string `json:"egressIPs,omitempty"`
	// NamespaceSelector selects the namespaces this EgressIP applies to.
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`
	// PodSelector selects pods within the selected namespaces; if empty, selects all.
	PodSelector *metav1.LabelSelector `json:"podSelector,omitempty"`
}
