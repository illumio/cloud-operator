// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
)

var resourceList = []string{
	"ciliumclusterwidenetworkpolicies",
	"ciliumnetworkpolicies",
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

// buildResourceApiGroupMap creates a mapping between Kubernetes resources and their API groups.
func buildResourceApiGroupMap(resources []string, clientset kubernetes.Interface, logger *zap.Logger) (map[string]string, error) {
	resourceAPIGroupMap := make(map[string]string)

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
		for _, version := range group.Versions {
			resourceList, err := discoveryClient.ServerResourcesForGroupVersion(version.GroupVersion)
			if err != nil {
				if apierrors.IsForbidden(err) {
					continue
				} else {
					return nil, err
				}
			}

			for _, resource := range resourceList.APIResources {
				if _, exists := resourceSet[resource.Name]; exists {
					if group.Name == "metrics.k8s.io" {
						logger.Info("Skipping this as it causes issues with discovery",
							zap.String("group", group.Name),
							zap.String("resource", resource.Name),
						)

						continue
					}

					resourceAPIGroupMap[resource.Name] = group.Name
				}
			}
		}
	}

	return resourceAPIGroupMap, nil
}

type watcherInfo struct {
	resource        string
	apiGroup        string
	resourceVersion string
}
