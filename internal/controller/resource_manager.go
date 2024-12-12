// Copyright 2024 Illumio, Inc. All Rights Reserved.
package controller

import (
	"context"
	"sync"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
)

// ResourceManager encapsulates components for listing and managing Kubernetes resources.
type ResourceManager struct {
	// Logger provides strucuted logging interface.
	logger *zap.SugaredLogger
	// DynamicClient offers generic Kubernetes API operations.
	dynamicClient dynamic.Interface
	// streamManager abstracts logic related to starting, using, and managing streams.
	streamManager *streamManager
}

// DynamicListAndWatchResources lists and watches the specified resource dynamically, managing context cancellation and synchronization with wait groups.
func (r *ResourceManager) DyanmicListAndWatchResources(ctx context.Context, cancel context.CancelFunc, resource string, apiGroup string, allResourcesSnapshotted *sync.WaitGroup, snapshotCompleted *sync.WaitGroup) {
	resourceListVersion, err := r.DynamicListResources(ctx, resource, apiGroup)
	if err != nil {
		allResourcesSnapshotted.Done()
		r.logger.Errorw("Unable to list resources", "error", err)
		cancel()
		return
	}
	allResourcesSnapshotted.Done()
	snapshotCompleted.Wait()
	// Here intiatate the watch event
	watchOptions := metav1.ListOptions{
		Watch:           true,
		ResourceVersion: resourceListVersion,
	}
	// Prevent us from overwhelming K8 api
	limiter := rate.NewLimiter(1, 5)
	err = limiter.Wait(ctx)
	if err != nil {
		r.logger.Errorw("Cannot wait using rate limiter", "error", err)
		cancel()
		return
	}

	err = r.watchEvents(ctx, resource, apiGroup, watchOptions)
	if err != nil {
		r.logger.Errorw("Unable to watch events", "error", err)
		cancel()
		return
	}
}

// DynamicListResources lists a specifed resource dynamically and sends down the current gRPC stream.
func (r *ResourceManager) DynamicListResources(ctx context.Context, resource string, apiGroup string) (string, error) {
	objGVR := schema.GroupVersionResource{Group: apiGroup, Version: "v1", Resource: resource}
	objs, resourceListVersion, err := r.ListResources(ctx, objGVR, metav1.NamespaceAll)
	if err != nil {
		return "", err
	}
	for _, obj := range objs {
		metadataObj, err := convertMetaObjectToMetadata(ctx, r.logger, obj, resource)
		if err != nil {
			r.logger.Errorw("Cannot convert object metadata", "error", err)
			return "", err
		}
		err = sendObjectData(r.streamManager, metadataObj)
		if err != nil {
			r.logger.Errorw("Cannot send object metadata", "error", err)
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

// watchEvents watches Kubernetes resources and updates cache based on events.
// Any occurring errors are sent through errChanWatch. The watch stops when ctx is cancelled.
func (r *ResourceManager) watchEvents(ctx context.Context, resource string, apiGroup string, watchOptions metav1.ListOptions) error {
	objGVR := schema.GroupVersionResource{Group: apiGroup, Version: "v1", Resource: resource}
	watcher, err := r.dynamicClient.Resource(objGVR).Namespace(metav1.NamespaceAll).Watch(ctx, watchOptions)
	if err != nil {
		r.logger.Errorw("Error setting up watch on resource", "error", err)
		return err
	}
	for event := range watcher.ResultChan() {
		switch event.Type {
		case watch.Error:
			r.logger.Errorw("Watcher event has returned an error", "error", err)
			return err
		case watch.Bookmark:
			continue
		default:
		}
		convertedData, err := getObjectMetadataFromRuntimeObject(event.Object)
		if err != nil {
			r.logger.Errorw("Cannot convert runtime.Object to metav1.ObjectMeta", "error", err)
			return err
		}
		metadataObj, err := convertMetaObjectToMetadata(ctx, r.logger, *convertedData, resource)
		if err != nil {
			r.logger.Errorw("Cannot convert object metadata", "error", err)
			return err
		}
		err = streamMutationObjectData(r.streamManager, metadataObj, event.Type)
		if err != nil {
			r.logger.Errorw("Cannot send resource mutation", "error", err)
			return err
		}

	}
	return nil
}

// FetchResources retrieves unstructured resources from the K8s API.
func (r *ResourceManager) FetchResources(ctx context.Context, resource schema.GroupVersionResource, namespace string) (*unstructured.UnstructuredList, error) {
	unstructuredResources, err := r.dynamicClient.Resource(resource).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		r.logger.Errorw("Cannot list resource", "error", err, "kind", resource)
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
			r.logger.Errorw("Cannot get Metadata from resource", "error", err)
			return nil, err
		}
		objectMetas = append(objectMetas, *objMeta)
	}
	return objectMetas, nil
}

// ListResources fetches resources of a specified type and namespace, returning their ObjectMeta,
// the last resource version observed, and any error encountered.
func (r *ResourceManager) ListResources(ctx context.Context, resource schema.GroupVersionResource, namespace string) ([]metav1.ObjectMeta, string, error) {
	unstructuredResources, err := r.FetchResources(ctx, resource, namespace)
	if err != nil {
		return nil, "", err
	}

	objectMetas, err := r.ExtractObjectMetas(unstructuredResources)
	if err != nil {
		return nil, "", err
	}

	return objectMetas, unstructuredResources.GetResourceVersion(), nil
}
