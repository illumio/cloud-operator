// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
)

var resourceList = []string{
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
	"services",
	"statefulsets",
}

// ResourceInfo holds the API group and preferred version for a resource.
type ResourceInfo struct {
	Group   string
	Version string
}

// buildResourceApiGroupMap creates a mapping between Kubernetes resources and their API groups with preferred versions.
func buildResourceApiGroupMap(resources []string, clientset kubernetes.Interface, logger *zap.Logger) (map[string]ResourceInfo, error) {
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

		// Query only the preferred version rather than iterating all group.Versions.
		// All resources in our resource list always present in the preferred version,
		resourceList, err := discoveryClient.ServerResourcesForGroupVersion(group.PreferredVersion.GroupVersion)
		if err != nil {
			if apierrors.IsForbidden(err) {
				continue
			}

			return nil, err
		}

		for _, resource := range resourceList.APIResources {
			if _, exists := resourceSet[resource.Name]; exists {
				resourceAPIGroupMap[resource.Name] = ResourceInfo{
					Group:   group.Name,
					Version: group.PreferredVersion.Version,
				}
			}
		}
	}

	return resourceAPIGroupMap, nil
}

type watcherInfo struct {
	resource        string // plural-lowercase (e.g., "pods")
	apiGroup        string
	apiVersion      string
	resourceVersion string
}
