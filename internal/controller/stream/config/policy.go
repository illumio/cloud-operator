// Copyright 2026 Illumio, Inc. All Rights Reserved.

package config

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// policyGVRCache defines supported policy kinds and caches their GVR after discovery.
var policyGVRCache = map[string]schema.GroupVersionResource{
	"CiliumNetworkPolicy":            {},
	"CiliumClusterwideNetworkPolicy": {},
	"NetworkPolicy":                  {},
}

// apiGroupVersions maps API groups to their versions for non-v1 resources.
var apiGroupVersions = map[string]string{
	"cilium.io": "v2",
}

func getVersionForGroup(group string) string {
	if version, ok := apiGroupVersions[group]; ok {
		return version
	}

	return "v1"
}

func InitPolicyGVRCache(clientset kubernetes.Interface, logger *zap.Logger) error {
	discoveryClient := clientset.Discovery()

	apiGroups, err := discoveryClient.ServerGroups()
	if err != nil {
		logger.Error("Error fetching API groups", zap.Error(err))

		return err
	}

	for _, group := range apiGroups.Groups {
		for _, version := range group.Versions {
			resourceList, err := discoveryClient.ServerResourcesForGroupVersion(version.GroupVersion)
			if err != nil {
				if apierrors.IsForbidden(err) {
					continue
				}

				return err
			}

			for _, resource := range resourceList.APIResources {
				if _, exists := policyGVRCache[resource.Kind]; exists {
					policyGVRCache[resource.Kind] = schema.GroupVersionResource{
						Group:    group.Name,
						Version:  getVersionForGroup(group.Name),
						Resource: resource.Name,
					}
				}
			}
		}
	}

	return nil
}

// getGVR returns the cached GroupVersionResource for a kind.
func getGVR(kind string) (schema.GroupVersionResource, error) {
	gvr, ok := policyGVRCache[kind]
	if !ok || gvr.Resource == "" {
		return schema.GroupVersionResource{}, fmt.Errorf("unknown or unsupported kind: %s", kind)
	}

	return gvr, nil
}

// CreatePolicy applies a network policy to the cluster.
func CreatePolicy(ctx context.Context, dynamicClient dynamic.Interface, logger *zap.Logger, policyData *pb.NetworkPolicyData) error {
	var obj unstructured.Unstructured
	if err := json.Unmarshal(policyData.GetResource(), &obj.Object); err != nil {
		return fmt.Errorf("failed to unmarshal policy JSON: %w", err)
	}

	gvr, err := getGVR(policyData.GetKind())
	if err != nil {
		return err
	}

	namespace := policyData.GetNamespace()

	logger.Info("Creating network policy",
		zap.String("id", policyData.GetId()),
		zap.String("name", policyData.GetName()),
		zap.String("namespace", namespace),
		zap.String("kind", policyData.GetKind()),
	)

	if namespace == "" {
		_, err = dynamicClient.Resource(gvr).Create(ctx, &obj, metav1.CreateOptions{})
	} else {
		_, err = dynamicClient.Resource(gvr).Namespace(namespace).Create(ctx, &obj, metav1.CreateOptions{})
	}

	if err != nil {
		return fmt.Errorf("failed to create policy %s: %w", policyData.GetId(), err)
	}

	return nil
}

// DeletePolicy removes a network policy from the cluster.
func DeletePolicy(ctx context.Context, dynamicClient dynamic.Interface, logger *zap.Logger, policyData *pb.NetworkPolicyData) error {
	gvr, err := getGVR(policyData.GetKind())
	if err != nil {
		return err
	}

	namespace := policyData.GetNamespace()
	name := policyData.GetName()

	logger.Info("Deleting network policy",
		zap.String("id", policyData.GetId()),
		zap.String("name", name),
		zap.String("namespace", namespace),
		zap.String("kind", policyData.GetKind()),
	)

	if namespace == "" {
		err = dynamicClient.Resource(gvr).Delete(ctx, name, metav1.DeleteOptions{})
	} else {
		err = dynamicClient.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	}

	if err != nil {
		return fmt.Errorf("failed to delete policy %s: %w", policyData.GetId(), err)
	}

	return nil
}
