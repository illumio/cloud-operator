// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import "testing"

func TestGetAWSKindFromResource(t *testing.T) {
	tests := []struct {
		name         string
		resourceName string
		expected     string
	}{
		{
			name:         "clusternetworkpolicies returns ClusterNetworkPolicy",
			resourceName: "clusternetworkpolicies",
			expected:     "ClusterNetworkPolicy",
		},
		{
			name:         "securitygrouppolicies returns SecurityGroupPolicy",
			resourceName: "securitygrouppolicies",
			expected:     "SecurityGroupPolicy",
		},
		{
			name:         "unknown resource returns empty string",
			resourceName: "pods",
			expected:     "",
		},
		{
			name:         "cilium resource returns empty string",
			resourceName: "ciliumnetworkpolicies",
			expected:     "",
		},
		{
			name:         "empty string returns empty string",
			resourceName: "",
			expected:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getAWSKindFromResource(tt.resourceName)
			if got != tt.expected {
				t.Errorf("getAWSKindFromResource(%q) = %q, want %q", tt.resourceName, got, tt.expected)
			}
		})
	}
}

func TestGetVersionForGroup(t *testing.T) {
	tests := []struct {
		name     string
		group    string
		expected string
	}{
		{
			name:     "core API group returns v1",
			group:    "",
			expected: "v1",
		},
		{
			name:     "apps group returns v1",
			group:    "apps",
			expected: "v1",
		},
		{
			name:     "networking.k8s.io group returns v1",
			group:    "networking.k8s.io",
			expected: "v1",
		},
		{
			name:     "cilium.io group returns v2",
			group:    "cilium.io",
			expected: "v2",
		},
		{
			name:     "networking.k8s.aws group returns v1alpha1",
			group:    "networking.k8s.aws",
			expected: "v1alpha1",
		},
		{
			name:     "vpcresources.k8s.aws group returns v1beta1",
			group:    "vpcresources.k8s.aws",
			expected: "v1beta1",
		},
		{
			name:     "unknown group returns v1",
			group:    "unknown.io",
			expected: "v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getVersionForGroup(tt.group)
			if got != tt.expected {
				t.Errorf("getVersionForGroup(%q) = %q, want %q", tt.group, got, tt.expected)
			}
		})
	}
}
