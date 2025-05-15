package aks

func IsAKSManagedHubble(annotations map[string]string) bool {
	releaseName := annotations["meta.helm.sh/release-name"]
	return releaseName == "aks-managed-hubble"
}
