// Copyright 2024 Illumio, Inc. All Rights Reserved.
package controller

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// ResourceManager encapsulates components for listing and managing Kubernetes resources.
type ResourceManager struct {
	// Clientset providing accees to k8s api.
	clientset *kubernetes.Clientset
	// Logger provides strucuted logging interface.
	logger *zap.Logger
	// DynamicClient offers generic Kubernetes API operations.
	dynamicClient dynamic.Interface
	// streamManager abstracts logic related to starting, using, and managing streams.
	streamManager *streamManager
}

// TODO: Make a struct with the ClientSet as a field, and convertMetaObjectToMetadata, getPodIPAddresses, getProviderIdNodeSpec should be methods of that struct.

// ListCurrentK8sWorkloads lists the current state of a specified Kubernetes resource
// using the dynamic client, and returns its latest resourceVersion.
func (r *ResourceManager) ListCurrentK8sWorkloads(ctx context.Context, resource string, apiGroup string) (string, error) {
	resourceVersion, err := r.DynamicListResources(ctx, r.logger, resource, apiGroup)
	if err != nil {
		r.logger.Error("Failed to list resource", zap.String("resource", resource), zap.Error(err))
		return "", err
	}
	return resourceVersion, nil
}

// WatchK8sWorkloads initiates a watch stream for the specified Kubernetes resource starting from the given resourceVersion.
// This function blocks until the watch ends or the context is canceled.
func (r *ResourceManager) WatchK8sWorkloads(ctx context.Context, cancel context.CancelFunc, resource string, apiGroup string, resourceVersion string) {
	defer cancel()

	// Here intiatate the watch event
	watchOptions := metav1.ListOptions{
		Watch:           true,
		ResourceVersion: resourceVersion,
	}

	// Prevent us from overwhelming K8 api
	limiter := rate.NewLimiter(1, 5)
	err := limiter.Wait(ctx)
	if err != nil {
		r.logger.Error("Cannot wait using rate limiter", zap.Error(err))
		return
	}

	err = r.watchEvents(ctx, resource, apiGroup, watchOptions)
	if err != nil {
		r.logger.Error("Watch failed", zap.String("resource", resource), zap.Error(err))
		return
	}
}

// DynamicListResources lists a specifed resource dynamically and sends down the current gRPC stream.
func (r *ResourceManager) DynamicListResources(ctx context.Context, logger *zap.Logger, resource string, apiGroup string) (string, error) {
	objGVR := schema.GroupVersionResource{Group: apiGroup, Version: "v1", Resource: resource}
	objs, resourceListVersion, resourceK8sKind, err := r.ListResources(ctx, objGVR, metav1.NamespaceAll)
	if err != nil {
		return "", err
	}
	for _, obj := range objs {
		metadataObj, err := convertMetaObjectToMetadata(logger, ctx, obj, r.clientset, resourceK8sKind)
		if err != nil {
			r.logger.Error("Cannot convert object metadata", zap.Error(err))
			return "", err
		}
		err = r.streamManager.sendObjectData(logger, metadataObj)
		if err != nil {
			r.logger.Error("Cannot send object metadata", zap.Error(err))
			return "", err
		}
	}

	select {
	case <-ctx.Done():
		return "", err
	default:
	}
	return resourceListVersion, nil
}

// getErrFromWatchEvent returns an error if the watch event is of type Error.
// Includes the 'code', 'reason', and 'message'. If the watch event is NOT of
// type Error then return nil
func getErrFromWatchEvent(event watch.Event) error {
	if event.Object == nil {
		return nil
	}
	if event.Type != watch.Error {
		return nil
	}

	status, ok := event.Object.(*metav1.Status)
	if !ok {
		return fmt.Errorf("unexpected error type: %T", event.Object)
	}

	return fmt.Errorf("code: %d, reason: %s, message: %s", status.Code, status.Reason, status.Message)
}

// watchEvents watches Kubernetes resources. The second part of the of the "list
// and watch" strategy.
// Any occurring errors are sent through errChanWatch. The watch stops when ctx is cancelled.
func (r *ResourceManager) watchEvents(ctx context.Context, resource string, apiGroup string, watchOptions metav1.ListOptions) error {
	logger := r.logger.With(
		zap.String("api_group", apiGroup),
		zap.String("resource", resource),
	)

	objGVR := schema.GroupVersionResource{Group: apiGroup, Version: "v1", Resource: resource}
	watcher, err := r.dynamicClient.Resource(objGVR).Namespace(metav1.NamespaceAll).Watch(ctx, watchOptions)
	if err != nil {
		logger.Error("Error setting up watch on resource", zap.Error(err))
		return err
	}

	for {
		select {
		case <-ctx.Done():
			logger.Debug("Disconnected from CloudSecure",
				zap.String("reason", "context cancelled"),
			)
			return ctx.Err()

		case event := <-watcher.ResultChan():
			// Exhaustive enum check on event type. We only want to report mutations
			switch event.Type {
			case watch.Error:
				err := getErrFromWatchEvent(event)
				logger.Error("Watcher event has returned an error", zap.Error(err))
				return err
			case watch.Bookmark:
				continue
			case watch.Added, watch.Modified, watch.Deleted:
			default:
				logger.Debug("Received unknown watch event", zap.String("type", string(event.Type)))
				continue
			}

			// Type gymnastics: turn the watch.Event into a 'KubernetesObjectData'
			convertedData, err := getObjectMetadataFromRuntimeObject(event.Object)
			if err != nil {
				logger.Error("Cannot convert runtime.Object to metav1.ObjectMeta", zap.Error(err))
				return err
			}
			resource := event.Object.GetObjectKind().GroupVersionKind().Kind
			metadataObj, err := convertMetaObjectToMetadata(logger, ctx, *convertedData, r.clientset, resource)
			if err != nil {
				logger.Error("Cannot convert object metadata", zap.Error(err))
				return err
			}

			// Helper function: type gymnastics + send the KubernetesObjectData out on the wire
			err = r.streamManager.streamMutationObjectData(logger, metadataObj, event.Type)
			if err != nil {
				logger.Error("Cannot send resource mutation", zap.Error(err))
				return err
			}
		}
	}
}

// FetchResources retrieves unstructured resources from the K8s API.
func (r *ResourceManager) FetchResources(ctx context.Context, resource schema.GroupVersionResource, namespace string) (*unstructured.UnstructuredList, error) {
	unstructuredResources, err := r.dynamicClient.Resource(resource).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		r.logger.Error("Cannot list resource", zap.Stringer("kind", resource), zap.Error(err))
		return nil, err
	}
	return unstructuredResources, nil
}

// ExtractObjectMetas extracts ObjectMeta from a list of unstructured resources.
func (r *ResourceManager) ExtractObjectMetas(resources *unstructured.UnstructuredList) ([]metav1.ObjectMeta, error) {
	objectMetas := make([]metav1.ObjectMeta, 0, len(resources.Items))
	for _, item := range resources.Items {
		objMeta, err := getMetadatafromResource(r.logger, item)
		if err != nil {
			r.logger.Error("Cannot get Metadata from resource", zap.Error(err))
			return nil, err
		}
		objectMetas = append(objectMetas, *objMeta)
	}
	return objectMetas, nil
}

// ListResources fetches resources of a specified type and namespace, returning their ObjectMeta,
// the last resource version observed, and any error encountered.
func (r *ResourceManager) ListResources(ctx context.Context, resource schema.GroupVersionResource, namespace string) ([]metav1.ObjectMeta, string, string, error) {
	unstructuredResources, err := r.FetchResources(ctx, resource, namespace)
	if err != nil {
		return nil, "", "", err
	}

	objectMetas, err := r.ExtractObjectMetas(unstructuredResources)
	if err != nil {
		return nil, "", "", err
	}

	return objectMetas, unstructuredResources.GetResourceVersion(), removeListSuffix(unstructuredResources.GetKind()), nil
}

// removeListSuffix removes the "List" suffix from a given string
// Ex: PodList -> Pod, StafefulSetList -> StatefulSet
func removeListSuffix(s string) string {
	if strings.HasSuffix(s, "List") {
		return s[:len(s)-4] // Remove the last 4 characters ("List")
	}
	return s
}
