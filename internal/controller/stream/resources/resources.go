// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"cmp"
	"slices"

	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"

	"github.com/illumio/cloud-operator/internal/convert"
	"github.com/illumio/cloud-operator/internal/convert/anp"
	"github.com/illumio/cloud-operator/internal/convert/awsvpccni"
	"github.com/illumio/cloud-operator/internal/convert/cilium"
	"github.com/illumio/cloud-operator/internal/convert/ovn"
)

// ManagedResources lists the resources managed by the reconciler.
var ManagedResources = []schema.GroupResource{
	{Group: cilium.APIGroup, Resource: "ciliumcidrgroups"},
	{Group: cilium.APIGroup, Resource: "ciliumclusterwidenetworkpolicies"},
	{Group: cilium.APIGroup, Resource: "ciliumnetworkpolicies"},
	{Group: awsvpccni.APIGroup, Resource: "clusternetworkpolicies"},
}

// resourceList lists every resource the resource stream watches. Resources are
// identified by API group and plural name together: the same plural name can be
// served by more than one group (gateway.networking.k8s.io/gateways and
// networking.istio.io/gateways, core nodes and config.openshift.io/nodes), and
// only the listed group is watched.
//
// ApplicationNetworkPolicy is intentionally excluded from ManagedResources: it is ingest-only.
var resourceList = slices.Concat(ManagedResources, []schema.GroupResource{
	{Group: "", Resource: "endpoints"},
	{Group: "", Resource: "namespaces"},
	{Group: "", Resource: "nodes"},
	{Group: "", Resource: "pods"},
	{Group: "", Resource: "replicationcontrollers"},
	{Group: "", Resource: "serviceaccounts"},
	{Group: "", Resource: "services"},
	{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"},
	{Group: "apps", Resource: "daemonsets"},
	{Group: "apps", Resource: "deployments"},
	{Group: "apps", Resource: "replicasets"},
	{Group: "apps", Resource: "statefulsets"},
	{Group: "batch", Resource: "cronjobs"},
	{Group: "batch", Resource: "jobs"},
	{Group: "elbv2.k8s.aws", Resource: "targetgroupbindings"},
	{Group: convert.GatewayAPIGroup, Resource: "gatewayclasses"},
	{Group: convert.GatewayAPIGroup, Resource: "gateways"},
	{Group: convert.GatewayAPIGroup, Resource: "grpcroutes"},
	{Group: convert.GatewayAPIGroup, Resource: "httproutes"},
	{Group: convert.GatewayAPIGroup, Resource: "tcproutes"},
	{Group: convert.GatewayAPIGroup, Resource: "tlsroutes"},
	{Group: convert.GatewayAPIGroup, Resource: "udproutes"},
	{Group: "metallb.io", Resource: "servicebgpstatuses"},
	{Group: "metallb.io", Resource: "servicel2statuses"},
	{Group: "networking.gke.io", Resource: "servicenetworkendpointgroups"},
	{Group: "networking.k8s.io", Resource: "ingressclasses"},
	{Group: "networking.k8s.io", Resource: "ingresses"},
	{Group: "networking.k8s.io", Resource: "networkpolicies"},
	{Group: anp.APIGroup, Resource: "adminnetworkpolicies"},
	{Group: anp.APIGroup, Resource: "baselineadminnetworkpolicies"},
	{Group: awsvpccni.APIGroup, Resource: "applicationnetworkpolicies"},
	{Group: ovn.APIGroup, Resource: "egressfirewalls"},
	{Group: ovn.APIGroup, Resource: "egressips"},
})

// ResourceInfo holds the API group and preferred version for a resource.
type ResourceInfo struct {
	Group   string
	Version string
}

// BuildResourceAPIGroupMap creates a mapping between Kubernetes resources and their API groups with preferred versions.
// Exported for use by the reconciler. Only each group's preferred version is
// searched, so the reconciler applies objects at the version the server prefers.
func BuildResourceAPIGroupMap(resources []schema.GroupResource, clientset kubernetes.Interface, logger *zap.Logger) (map[schema.GroupResource]ResourceInfo, error) {
	return buildResourceAPIGroupMap(resources, clientset, logger, false)
}

// buildResourceAPIGroupMap maps each wanted resource to the version it is served at.
// With searchAllVersions, a wanted resource missing from its group's preferred
// version is looked up in the group's other versions, in server priority order:
// MetalLB prefers metallb.io/v1beta2 but serves ServiceL2Status only in v1beta1,
// and networking.gke.io can prefer v1 while ServiceNetworkEndpointGroup is v1beta1.
func buildResourceAPIGroupMap(resources []schema.GroupResource, clientset kubernetes.Interface, logger *zap.Logger, searchAllVersions bool) (map[schema.GroupResource]ResourceInfo, error) {
	resourceAPIGroupMap := make(map[schema.GroupResource]ResourceInfo)

	resourceSet := make(map[schema.GroupResource]struct{})
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
// resource already found at a higher-priority version is kept.
func addResources(resourceAPIGroupMap map[schema.GroupResource]ResourceInfo, resourceSet map[schema.GroupResource]struct{}, groupName, version string, apiResources []metav1.APIResource) {
	for _, resource := range apiResources {
		groupResource := schema.GroupResource{Group: groupName, Resource: resource.Name}

		if _, wanted := resourceSet[groupResource]; !wanted {
			continue
		}

		if _, found := resourceAPIGroupMap[groupResource]; found {
			continue
		}

		resourceAPIGroupMap[groupResource] = ResourceInfo{
			Group:   groupName,
			Version: version,
		}
	}
}

// hasMissingResource reports whether a wanted resource in groupName has not
// been found yet.
func hasMissingResource(resourceAPIGroupMap map[schema.GroupResource]ResourceInfo, resourceSet map[schema.GroupResource]struct{}, groupName string) bool {
	for groupResource := range resourceSet {
		if groupResource.Group != groupName {
			continue
		}

		if _, found := resourceAPIGroupMap[groupResource]; !found {
			return true
		}
	}

	return false
}

// watcherInfo pairs a watcher with the resourceVersion of its initial list, from
// which its watch starts.
type watcherInfo struct {
	watcher         *Watcher
	resourceVersion string
}

// compareGroupResources orders resources by API group, then plural name, so
// watchers start in a stable order.
func compareGroupResources(a, b schema.GroupResource) int {
	if c := cmp.Compare(a.Group, b.Group); c != 0 {
		return c
	}

	return cmp.Compare(a.Resource, b.Resource)
}
