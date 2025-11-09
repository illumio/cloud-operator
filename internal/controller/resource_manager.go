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

	r.logger.Debug("Successfully sent k8s resources", zap.Int("count", len(objs)))

	select {
	case <-ctx.Done():
		return "", err
	default:
	}

	return resourceListVersion, nil
}

// getErrFromWatchEvent returns an error if the watch event is of type Error.
// Returns a properly typed Kubernetes StatusError so error checking functions like
// apierrors.IsResourceExpired() work correctly.
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

	// Return a properly typed StatusError so apierrors.Is* functions work
	return &apierrors.StatusError{ErrStatus: *status}
}

// handleWatchError processes errors from watch events.
// Returns true if the watcher should be restarted (break watcherLoop), or returns an error to exit watchEvents entirely.
func (r *ResourceManager) handleWatchError(err error, lastResourceVersion string, logger *zap.Logger) (shouldBreak bool, returnErr error) {
	//nolint:exhaustive // we intentionally only care about specific status reasons
	switch apierrors.ReasonForError(err) {
	case metav1.StatusReasonExpired:
		// If we got an expired resource version error, return error to restart the stream
		// with a new list-and-watch cycle.
		logger.Warn("Resource version expired, restarting list-and-watch from current state",
			zap.String("expired_resource_version", lastResourceVersion))

		return false, err
	default:
		logger.Error("Error processing watch event", zap.Error(err))
		// For other errors, just recreate the watcher from last known RV.
		return true, nil
	}
}

// watchEvents watches Kubernetes resources. The second part of the "list and watch" strategy.
// Stops when ctx is cancelled.
//
//nolint:gocognit // function is complex by nature (watch loop), splitting would reduce readability
func (r *ResourceManager) watchEvents(ctx context.Context, resourceVersion string, mutationChan chan *pb.KubernetesResourceMutation) error {
	logger := r.logger

	lastResourceVersion := resourceVersion

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

			logger.Debug("Restarting watcher", zap.String("resource_version", lastResourceVersion))
		}

		var err error

		watcher, err = r.newWatcher(ctx, lastResourceVersion, logger)
		if err != nil {
			return err
		}

	watcherLoop:
		for {
			select {
			case <-ctx.Done():
				logger.Debug("Disconnected from CloudSecure (context canceled)")

				return ctx.Err()

			case event, ok := <-watcher.ResultChan():
				if !ok {
					logger.Debug("Watcher channel closed")

					break watcherLoop
				}

				newResourceVersion, eventIsMutation, err := r.handleWatchEvent(ctx, event, mutationChan, logger)
				if err != nil {
					shouldBreak, returnErr := r.handleWatchError(err, lastResourceVersion, logger)
					if returnErr != nil {
						return returnErr
					}

					if shouldBreak {
						break watcherLoop
					}
				}

				if newResourceVersion != "" {
					lastResourceVersion = newResourceVersion
				}

				if eventIsMutation {
					mutationCount += 1
				}

			case <-time.After(60 * time.Second):
				logger.Debug("Processed mutations checkpoint", zap.Duration("period", 60*time.Second), zap.Int("mutation_count", mutationCount))

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
			r.logger.Error("Cannot get metadata from resource", zap.Error(err))

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
) (string, bool, error) {
	switch event.Type {
	case watch.Error:
		err := getErrFromWatchEvent(event)

		return "", false, fmt.Errorf("watcher returned an error: %w", err)

	case watch.Bookmark:
		logger.Debug("Received bookmark from watcher")

		resourceVersion, err := getResourceVersionFromBookmark(event)
		if err != nil {
			return "", false, err
		}

		return resourceVersion, false, nil

	case watch.Added, watch.Modified, watch.Deleted:
		logger.Debug("Received mutation from watcher", zap.String("type", string(event.Type)))

		resourceVersion, err := r.processMutation(ctx, event, mutationChan)

		return resourceVersion, true, err

	default:
		return "", false, fmt.Errorf("watcher returned an unknown or empty watch event of type %s", string(event.Type))
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
func (r *ResourceManager) processMutation(ctx context.Context, event watch.Event, mutationChan chan *pb.KubernetesResourceMutation) (string, error) {
	convertedData, err := getObjectMetadataFromRuntimeObject(event.Object)
	if err != nil {
		return "", fmt.Errorf("failed to convert runtime.Object to metav1.ObjectMeta: %w", err)
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
