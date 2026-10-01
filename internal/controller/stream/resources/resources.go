// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"slices"

	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ManagedResourceNames lists the plural resource names managed by the reconciler.
var ManagedResourceNames = []string{
	"ciliumcidrgroups",
	"ciliumclusterwidenetworkpolicies",
	"ciliumnetworkpolicies",
	"clusternetworkpolicies",
}

// ApplicationNetworkPolicy is intentionally excluded: it is ingest-only.
var resourceList = slices.Concat(ManagedResourceNames, []string{
	"applicationnetworkpolicies",
	"cronjobs",
	"customresourcedefinitions",
	"daemonsets",
	"deployments",
	"endpoints",
	"gateways",
	"gatewayclasses",
	"httproutes",
	"ingresses",
	"ingressclasses",
	"jobs",
	"namespaces",
	"networkpolicies",
	"nodes",
	"pods",
	"replicasets",
	"replicationcontrollers",
	"serviceaccounts",
	"servicebgpstatuses",
	"servicel2statuses",
	"servicenetworkendpointgroups",
	"services",
	"statefulsets",
	"targetgroupbindings",
})

// resourceGroups pins each resource name we discover to the API group we expect
// it in. Several plural names are also served by common CRD groups, which
// discovery returns after the built-in groups, so without the pin the CRD would
// replace the resource we mean: config.openshift.io/nodes and /ingresses,
// serving.knative.dev/services, crd.projectcalico.org/networkpolicies,
// networking.istio.io/gateways, policy.networking.k8s.io/clusternetworkpolicies.
// Every entry in resourceList must be listed here.
var resourceGroups = map[string]string{
	"applicationnetworkpolicies":       "networking.k8s.aws",
	"ciliumcidrgroups":                 "cilium.io",
	"ciliumclusterwidenetworkpolicies": "cilium.io",
	"ciliumnetworkpolicies":            "cilium.io",
	"clusternetworkpolicies":           "networking.k8s.aws",
	"cronjobs":                         "batch",
	"customresourcedefinitions":        "apiextensions.k8s.io",
	"daemonsets":                       "apps",
	"deployments":                      "apps",
	"endpoints":                        "",
	"gatewayclasses":                   "gateway.networking.k8s.io",
	"gateways":                         "gateway.networking.k8s.io",
	"httproutes":                       "gateway.networking.k8s.io",
	"ingressclasses":                   "networking.k8s.io",
	"ingresses":                        "networking.k8s.io",
	"jobs":                             "batch",
	"namespaces":                       "",
	"networkpolicies":                  "networking.k8s.io",
	"nodes":                            "",
	"pods":                             "",
	"replicasets":                      "apps",
	"replicationcontrollers":           "",
	"serviceaccounts":                  "",
	"servicebgpstatuses":               "metallb.io",
	"servicel2statuses":                "metallb.io",
	"servicenetworkendpointgroups":     "networking.gke.io",
	"services":                         "",
	"statefulsets":                     "apps",
	"targetgroupbindings":              "elbv2.k8s.aws",
}

// ResourceInfo holds the API group and preferred version for a resource.
type ResourceInfo struct {
	Group   string
	Version string
}

// BuildResourceAPIGroupMap creates a mapping between Kubernetes resources and their API groups with preferred versions.
// Exported for use by the reconciler. Only each group's preferred version is
// searched, so the reconciler applies objects at the version the server prefers.
func BuildResourceAPIGroupMap(resources []string, clientset kubernetes.Interface, logger *zap.Logger) (map[string]ResourceInfo, error) {
	return buildResourceAPIGroupMap(resources, clientset, logger, false)
}

// buildResourceAPIGroupMap maps each resource to its API group and version.
// With searchAllVersions, a pinned resource missing from its group's preferred
// version is looked up in the group's other versions, in server priority order:
// MetalLB prefers metallb.io/v1beta2 but serves ServiceL2Status only in v1beta1,
// and networking.gke.io can prefer v1 while ServiceNetworkEndpointGroup is v1beta1.
func buildResourceAPIGroupMap(resources []string, clientset kubernetes.Interface, logger *zap.Logger, searchAllVersions bool) (map[string]ResourceInfo, error) {
	resourceAPIGroupMap := make(map[string]ResourceInfo)

	resourceSet := make(map[string]struct{})
	for _, resource := range resources {
		resourceSet[resource] = struct{}{}
	}

	discoveryClient := clientset.Discovery()

	apiGroups, err := discoveryClient.ServerGroups()
	if err != nil {
		logger.Error("Error fetching API groups", zap.Error(err))

		return resourceAPIGroupMap, err
	}

	for _, group := range apiGroups.Groups {
		if group.Name == "metrics.k8s.io" {
			logger.Debug("Skipping metrics.k8s.io group as it causes issues with discovery")

			continue
		}

		resourceList, err := discoveryClient.ServerResourcesForGroupVersion(group.PreferredVersion.GroupVersion)
		if err != nil {
			if apierrors.IsForbidden(err) {
				continue
			}

			return nil, err
		}

		addResources(resourceAPIGroupMap, resourceSet, group.Name, group.PreferredVersion.Version, resourceList.APIResources)

		if !searchAllVersions {
			continue
		}

		for _, version := range group.Versions {
			if version.GroupVersion == group.PreferredVersion.GroupVersion {
				continue
			}

			if !hasMissingResource(resourceAPIGroupMap, resourceSet, group.Name) {
				break
			}

			resourceList, err := discoveryClient.ServerResourcesForGroupVersion(version.GroupVersion)
			if err != nil {
				if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) {
					continue
				}

				return nil, err
			}

			addResources(resourceAPIGroupMap, resourceSet, group.Name, version.Version, resourceList.APIResources)
		}
	}

	return resourceAPIGroupMap, nil
}

// addResources records the wanted resources served at groupName/version. A
// resource pinned to another group is ignored, and a resource already found at
// a higher-priority version is kept.
func addResources(resourceAPIGroupMap map[string]ResourceInfo, resourceSet map[string]struct{}, groupName, version string, apiResources []metav1.APIResource) {
	for _, resource := range apiResources {
		if _, wanted := resourceSet[resource.Name]; !wanted {
			continue
		}

		if pinnedGroup, pinned := resourceGroups[resource.Name]; pinned && pinnedGroup != groupName {
			continue
		}

		if existing, found := resourceAPIGroupMap[resource.Name]; found && existing.Group == groupName {
			continue
		}

		resourceAPIGroupMap[resource.Name] = ResourceInfo{
			Group:   groupName,
			Version: version,
		}
	}
}

// hasMissingResource reports whether a wanted resource pinned to groupName has
// not been found yet.
func hasMissingResource(resourceAPIGroupMap map[string]ResourceInfo, resourceSet map[string]struct{}, groupName string) bool {
	for resource := range resourceSet {
		if resourceGroups[resource] != groupName {
			continue
		}

		if _, found := resourceAPIGroupMap[resource]; !found {
			return true
		}
	}

	return false
}

type watcherInfo struct {
	resource        string // plural-lowercase (e.g., "pods")
	apiGroup        string
	apiVersion      string
	resourceVersion string
}
