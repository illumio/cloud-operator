// Copyright 2025 Illumio, Inc. All Rights Reserved.

package aks

// IsAKSManagedHubble checks if the Hubble Relay service annotations indicate it's managed by AKS.
// The annotations map is expected to come from a Kubernetes Service object, specifically
// from the service.Annotations field.
func IsAKSManagedHubble(annotations map[string]string) bool {
	releaseName := annotations["meta.helm.sh/release-name"]
	return releaseName == "aks-managed-hubble"
}
