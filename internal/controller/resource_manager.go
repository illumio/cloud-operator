// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// ResourceManagerConfig holds the configuration for creating a new ResourceManager.
type ResourceManagerConfig struct {
	ResourceName  string
	ApiGroup      string
	Clientset     *kubernetes.Clientset
	BaseLogger    *zap.Logger
	DynamicClient dynamic.Interface
	StreamManager *streamManager
	Limiter       *rate.Limiter
}

// ResourceManager encapsulates components for listing and managing Kubernetes resources.
type ResourceManager struct {
	// resourceName identifies which resource this manager handles
	resourceName string
	// apiGroup identifies the Kubernetes API group for this resource
	apiGroup string
	// Clientset providing access to k8s api.
	clientset *kubernetes.Clientset
	// Logger provides structured logging interface.
	logger *zap.Logger
	// DynamicClient offers generic Kubernetes API operations.
	dynamicClient dynamic.Interface
	// streamManager abstracts logic related to starting, using, and managing streams.
	streamManager *streamManager
	// limiter prevents concurrent watcher events from overwhelming k8s api server.
	limiter *rate.Limiter
}

// NewResourceManager creates a new [ResourceManager] for a specific resource type.
// The logger will automatically include the resource name in all log messages.
func NewResourceManager(config ResourceManagerConfig) *ResourceManager {
	// Create a logger with the resource name already included
	logger := config.BaseLogger.With(
		zap.String("resource", config.ResourceName),
		zap.String("api_group", config.ApiGroup),
	)

	return &ResourceManager{
		resourceName:  config.ResourceName,
		apiGroup:      config.ApiGroup,
		clientset:     config.Clientset,
		logger:        logger,
		dynamicClient: config.DynamicClient,
		streamManager: config.StreamManager,
		limiter:       config.Limiter,
	}
}

// TODO: Make a struct with the ClientSet as a field, and [convertMetaObjectToMetadata], [getPodIPAddresses], [getProviderIdNodeSpec] should be methods of that struct.

// WatchK8sResources initiates a watch stream for the specified Kubernetes resource starting from the given resourceVersion.
// This function blocks until the watch ends or the context is canceled.
func (r *ResourceManager) WatchK8sResources(ctx context.Context, cancel context.CancelFunc, resourceVersion string, mutationChan chan *pb.KubernetesResourceMutation) {
	defer cancel()

	err := r.limiter.Wait(ctx)
	if err != nil {
		r.logger.Error("Cannot wait using rate limiter", zap.Error(err))

		return
	}

	err = r.watchEvents(ctx, resourceVersion, mutationChan)
	if err != nil {
		r.logger.Error("Watch failed", zap.Error(err))

		return
	}
}

// DynamicListResources lists a specified resource dynamically and sends down the current gRPC stream.
func (r *ResourceManager) DynamicListResources(ctx context.Context, logger *zap.Logger, apiGroup string) (string, error) {
	objGVR := schema.GroupVersionResource{Group: apiGroup, Version: "v1", Resource: r.resourceName}

	objs, resourceListVersion, resourceK8sKind, err := r.ListResources(ctx, objGVR, metav1.NamespaceAll)
	if err != nil {
		return "", err
	}

	for _, obj := range objs {
		metadataObj := convertMetaObjectToMetadata(ctx, obj, r.clientset, resourceK8sKind)

		err = r.streamManager.sendObjectData(logger, metadataObj)
		if err != nil {
			r.logger.Error("Cannot send object metadata", zap.Error(err))

			return "", err
		}
	}

	r.logger.Debug("Successfully sent k8s workloads", zap.Int("count", len(objs)))

	select {
	case <-ctx.Done():
		return "", err
	default:
	}

	return resourceListVersion, nil
}

// getErrFromWatchEvent returns an error if the watch event is of type Error.
// Includes the 'code', 'reason', and 'message'. If the watch event is NOT of
// type Error then return nil.
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
func (r *ResourceManager) watchEvents(ctx context.Context, resourceVersion string, mutationChan chan *pb.KubernetesResourceMutation) error {
	logger := r.logger

	lastKnownResourceVersion := resourceVersion

	var watcher watch.Interface

	defer func() {
		if watcher != nil {
			watcher.Stop()
		}
	}()

	mutationCount := 0

	for {
		// Stop any existing watcher and create a new one from the last known resource version
		if watcher != nil {
			watcher.Stop()
			watcher = nil

			logger.Debug("Restarting watcher", zap.String("fromResourceVersion", lastKnownResourceVersion))
		}

		w, err := r.newWatcher(ctx, lastKnownResourceVersion, logger)
		if err != nil {
			return err
		}

		watcher = w

	watcherLoop:
		for {
			select {
			case <-ctx.Done():
				logger.Debug("Disconnected from CloudSecure", zap.String("reason", "context cancelled"))

				return ctx.Err()

			case event, ok := <-watcher.ResultChan():
				if !ok {
					logger.Debug("Watcher channel closed")

					break watcherLoop
				}

				restart, err := r.handleWatchEvent(ctx, event, mutationChan, logger, &lastKnownResourceVersion, &mutationCount)
				if err != nil {
					return err
				}

				if restart {
					break watcherLoop
				}

			case <-time.After(60 * time.Second):
				logger.Debug("Current mutation count", zap.Int("mutation_count", mutationCount))
				logger.Debug("Resetting mutation count")

				mutationCount = 0
			}
		}
	}
}

// FetchResources retrieves unstructured resources from the K8s API.
func (r *ResourceManager) FetchResources(ctx context.Context, resource schema.GroupVersionResource, namespace string) (*unstructured.UnstructuredList, error) {
	unstructuredResources, err := r.dynamicClient.Resource(resource).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		// Check if the error is related to forbidden access
		if apierrors.IsForbidden(err) {
			r.logger.Warn("Access forbidden for resource", zap.Stringer("kind", resource), zap.Error(err))
			// Gracefully handle forbidden errors by returning the wrapped error
			return nil, fmt.Errorf("access forbidden for resource %s: %w", resource.Resource, err)
		}

		// Log and return other errors as usual
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

// removeListSuffix removes the "List" suffix from a given string, e.g.,
// PodList -> Pod, StafefulSetList -> StatefulSet.
func removeListSuffix(s string) string {
	if strings.HasSuffix(s, "List") {
		return s[:len(s)-4] // Remove the last 4 characters ("List")
	}

	return s
}

// newWatcher creates a new Kubernetes watcher starting from the given resource version.
func (r *ResourceManager) newWatcher(ctx context.Context, resourceVersion string, logger *zap.Logger) (watch.Interface, error) {
	watchOptions := metav1.ListOptions{
		Watch:               true,
		ResourceVersion:     resourceVersion,
		AllowWatchBookmarks: true,
	}

	objGVR := schema.GroupVersionResource{Group: r.apiGroup, Version: "v1", Resource: r.resourceName}

	w, err := r.dynamicClient.Resource(objGVR).Namespace(metav1.NamespaceAll).Watch(ctx, watchOptions)
	if err != nil {
		logger.Error("Error setting up watch on resource", zap.Error(err))

		return nil, err
	}

	return w, nil
}

// handleWatchEvent processes a single watch.Event.
// It updates lastKnownResourceVersion for bookmarks, sends mutations for change events,
// and returns (restart=true) when the watcher should be restarted (empty/unknown types).
func (r *ResourceManager) handleWatchEvent(
	ctx context.Context,
	event watch.Event,
	mutationChan chan *pb.KubernetesResourceMutation,
	logger *zap.Logger,
	lastKnownResourceVersion *string,
	mutationCount *int,
) (bool, error) {
	switch event.Type {
	case watch.Error:
		err := getErrFromWatchEvent(event)
		logger.Error("Watcher event returned error", zap.Error(err))

		return false, err

	case watch.Bookmark:
		newResourceVersion, err := getResourceVersionFromBookmark(event)
		if err != nil {
			logger.Error("Failed to extract resourceVersion from bookmark", zap.Error(err))

			return false, err
		}

		*lastKnownResourceVersion = newResourceVersion
		logger.Debug("Received bookmark", zap.String("resource_version", *lastKnownResourceVersion))

		return false, nil

	case watch.Added, watch.Modified, watch.Deleted:
		logger.Debug("Received mutation event", zap.String("type", string(event.Type)))

		if resourceVersion, err := r.processMutation(ctx, event, mutationChan, logger); err != nil {
			return false, err
		} else if resourceVersion != "" {
			*lastKnownResourceVersion = resourceVersion
			logger.Debug("Updated lastKnownResourceVersion from mutation", zap.String("resource_version", resourceVersion))
		}

		*mutationCount++

		return false, nil

	default:
		// Treat empty and unknown types the same: restart the watcher
		logger.Debug("Received unknown or empty watch event", zap.String("type", string(event.Type)))

		return true, nil
	}
}

// getResourceVersionFromBookmark extracts the resourceVersion from a Bookmark event.
func getResourceVersionFromBookmark(event watch.Event) (string, error) {
	if event.Object == nil {
		return "", errors.New("k8s watcher bookmark event contains no object")
	}

	obj, ok := event.Object.(interface{ GetResourceVersion() string })
	if !ok {
		return "", errors.New("k8s watcher bookmark event contains no resource version")
	}

	resourceVersion := obj.GetResourceVersion()
	if resourceVersion == "" {
		return "", errors.New("k8s watcher bookmark event contains no resource version")
	}

	return resourceVersion, nil
}

// processMutation converts the event object to our metadata, builds a mutation, and sends it on the channel.
// It returns the resourceVersion from the event object so callers can persist progress.
func (r *ResourceManager) processMutation(ctx context.Context, event watch.Event, mutationChan chan *pb.KubernetesResourceMutation, logger *zap.Logger) (string, error) {
	convertedData, err := getObjectMetadataFromRuntimeObject(event.Object)
	if err != nil {
		logger.Error("Cannot convert runtime.Object to metav1.ObjectMeta", zap.Error(err))

		return "", err
	}

	resource := event.Object.GetObjectKind().GroupVersionKind().Kind
	metadataObj := convertMetaObjectToMetadata(ctx, *convertedData, r.clientset, resource)

	// Helper function: type gymnastics + send the KubernetesObjectData on the mutation channel
	mutation := r.streamManager.createMutationObject(metadataObj, event.Type)

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case mutationChan <- mutation:
	}

	return convertedData.GetResourceVersion(), nil
}
