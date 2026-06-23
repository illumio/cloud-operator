// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Local structs mirroring the AWS VPC CNI ClusterNetworkPolicy CRD spec
// (networking.k8s.aws/v1alpha1). We deserialize the unstructured object into
// these plain structs and then convert to proto. Unlike Cilium, the CRD uses
// the standard metav1.LabelSelector and pulls in no heavy dependency tree, so
// plain structs with matching JSON tags are sufficient.
//
// Schema: github.com/aws/amazon-network-policy-controller-k8s

// awsClusterNetworkPolicy mirrors the ClusterNetworkPolicy CRD top-level object.
type awsClusterNetworkPolicy struct {
	Spec awsClusterNetworkPolicySpec `json:"spec"`
}

// awsClusterNetworkPolicySpec mirrors ClusterNetworkPolicySpec.
type awsClusterNetworkPolicySpec struct {
	Priority int32            `json:"priority"`
	Tier     string           `json:"tier"`
	Subject  awsSubject       `json:"subject"`
	Ingress  []awsIngressRule `json:"ingress,omitempty"`
	Egress   []awsEgressRule  `json:"egress,omitempty"`
}

// awsSubject mirrors ClusterNetworkPolicySubject. Exactly one field is set.
type awsSubject struct {
	Namespaces *metav1.LabelSelector `json:"namespaces,omitempty"`
	Pods       *awsNamespacedPod     `json:"pods,omitempty"`
}

// awsNamespacedPod mirrors NamespacedPod.
type awsNamespacedPod struct {
	NamespaceSelector metav1.LabelSelector `json:"namespaceSelector"`
	PodSelector       metav1.LabelSelector `json:"podSelector"`
}

// awsIngressRule mirrors ClusterNetworkPolicyIngressRule.
type awsIngressRule struct {
	Name   string           `json:"name,omitempty"`
	Action string           `json:"action"`
	From   []awsIngressPeer `json:"from"`
	Ports  *[]awsPort       `json:"ports,omitempty"`
}

// awsEgressRule mirrors ClusterNetworkPolicyEgressRule.
type awsEgressRule struct {
	Name   string          `json:"name,omitempty"`
	Action string          `json:"action"`
	To     []awsEgressPeer `json:"to"`
	Ports  *[]awsPort      `json:"ports,omitempty"`
}

// awsIngressPeer mirrors ClusterNetworkPolicyIngressPeer.
type awsIngressPeer struct {
	Namespaces *metav1.LabelSelector `json:"namespaces,omitempty"`
	Pods       *awsNamespacedPod     `json:"pods,omitempty"`
}

// awsEgressPeer mirrors ClusterNetworkPolicyEgressPeer.
type awsEgressPeer struct {
	Namespaces  *metav1.LabelSelector `json:"namespaces,omitempty"`
	Pods        *awsNamespacedPod     `json:"pods,omitempty"`
	Networks    []string              `json:"networks,omitempty"`
	DomainNames []string              `json:"domainNames,omitempty"`
}

// awsPort mirrors ClusterNetworkPolicyPort. Exactly one field is set.
type awsPort struct {
	PortNumber *awsPortNumber `json:"portNumber,omitempty"`
	PortRange  *awsPortRange  `json:"portRange,omitempty"`
	NamedPort  *string        `json:"namedPort,omitempty"`
}

// awsPortNumber mirrors CNPPort.
type awsPortNumber struct {
	Protocol string `json:"protocol,omitempty"`
	Port     int32  `json:"port"`
}

// awsPortRange mirrors CNPPortRange.
type awsPortRange struct {
	Protocol string `json:"protocol,omitempty"`
	Start    int32  `json:"start"`
	End      int32  `json:"end"`
}
