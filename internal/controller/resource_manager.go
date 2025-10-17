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
	// Clientset providing accees to k8s api.
	clientset *kubernetes.Clientset
	// Logger provides strucuted logging interface.
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
func (r *ResourceManager) WatchK8sResources(ctx context.Context, cancel context.CancelFunc, apiGroup string, resourceVersion string, mutationChan chan *pb.KubernetesResourceMutation) {
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

	// Build watch options from the provided resource version
	watchOptions := metav1.ListOptions{
		Watch:               true,
		ResourceVersion:     resourceVersion,
		AllowWatchBookmarks: true,
	}

	lastKnownResourceVersion := resourceVersion

	var watcher watch.Interface

	defer func() {
		if watcher != nil {
			watcher.Stop()
		}
	}()

	mutationCount := 0

	for {
		// (Re)initialize the watcher using a helper
		w, err := r.startWatcher(ctx, r.apiGroup, lastKnownResourceVersion, watchOptions, watcher, logger)
		if err != nil {
			return err
		}

		watcher = w

		// Run the watcher loop until a restart is requested or an error occurs
		updatedRV, restart, err := r.runWatcherUntilRestart(ctx, watcher, lastKnownResourceVersion, mutationChan, logger, &mutationCount)
		if err != nil {
			return err
		}

		lastKnownResourceVersion = updatedRV

		if !restart {
			return nil
		}
	}
}

// runWatcherUntilRestart processes events from a watcher until it needs to be restarted or an error occurs.
// It returns the latest resourceVersion, whether a restart is needed, and any error encountered.
func (r *ResourceManager) runWatcherUntilRestart(
	ctx context.Context,
	watcher watch.Interface,
	lastKnownResourceVersion string,
	mutationChan chan *pb.KubernetesResourceMutation,
	logger *zap.Logger,
	mutationCount *int,
) (string, bool, error) {
	for {
		select {
		case <-ctx.Done():
			logger.Debug("Disconnected from CloudSecure", zap.String("reason", "context cancelled"))

			return lastKnownResourceVersion, false, ctx.Err()

		case event, ok := <-watcher.ResultChan():
			if !ok {
				logger.Debug("Watcher channel closed")
				logger.Debug("Restarting watcher", zap.String("fromResourceVersion", lastKnownResourceVersion))

				return lastKnownResourceVersion, true, nil
			}

			switch event.Type {
			case watch.Error:
				err := getErrFromWatchEvent(event)
				logger.Error("Watcher event returned error", zap.Error(err))

				return lastKnownResourceVersion, false, err

			case watch.Bookmark:
				newRV, err := updateRVFromBookmark(event)
				if err != nil {
					logger.Error("Failed to extract resourceVersion from bookmark", zap.Error(err))

					return lastKnownResourceVersion, false, err
				}

				lastKnownResourceVersion = newRV
				logger.Debug("Received bookmark", zap.String("resourceVersion", lastKnownResourceVersion))

				continue

			case watch.Added, watch.Modified, watch.Deleted:
				logger.Debug("Got watch event", zap.String("type", string(event.Type)))

			default:
				if event.Type == "" {
					logger.Debug("Received empty event type")
					logger.Debug("Restarting watcher", zap.String("fromResourceVersion", lastKnownResourceVersion))

					return lastKnownResourceVersion, true, nil
				}

				logger.Debug("Received unknown watch event", zap.String("type", string(event.Type)))

				continue
			}

			// Process mutations (only for Added/Modified/Deleted)
			if err := r.processMutation(ctx, event, mutationChan, logger); err != nil {
				return lastKnownResourceVersion, false, err
			}

			*mutationCount++

		case <-time.After(60 * time.Second):
			logger.Debug("Current mutation count", zap.Int("mutation_count", *mutationCount))
			logger.Debug("Resetting mutation count")

			*mutationCount = 0
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

// startWatcher (re)initializes the Kubernetes watch stream using the last known resourceVersion.
func (r *ResourceManager) startWatcher(ctx context.Context, apiGroup string, lastKnownRV string, watchOptions metav1.ListOptions, existing watch.Interface, logger *zap.Logger) (watch.Interface, error) {
	// Always resume from the last known resourceVersion.
	watchOptions.ResourceVersion = lastKnownRV
	// Ensure we get periodic bookmarks for advancing the resume point.
	watchOptions.AllowWatchBookmarks = true
	// Stop any existing watcher before creating a new one.
	if existing != nil {
		existing.Stop()
	}

	objGVR := schema.GroupVersionResource{Group: apiGroup, Version: "v1", Resource: r.resourceName}

	w, err := r.dynamicClient.Resource(objGVR).Namespace(metav1.NamespaceAll).Watch(ctx, watchOptions)
	if err != nil {
		logger.Error("Error setting up watch on resource", zap.Error(err))

		return nil, err
	}

	return w, nil
}

// updateRVFromBookmark extracts the resourceVersion from a Bookmark event.
func updateRVFromBookmark(event watch.Event) (string, error) {
	if event.Object == nil {
		return "", errors.New("bookmark object is nil")
	}

	if obj, ok := event.Object.(interface{ GetResourceVersion() string }); ok {
		if rv := obj.GetResourceVersion(); rv != "" {
			return rv, nil
		}
	}

	return "", errors.New("bookmark missing resourceVersion")
}

// processMutation converts the event object to our metadata, builds a mutation, and sends it on the channel.
func (r *ResourceManager) processMutation(ctx context.Context, event watch.Event, mutationChan chan *pb.KubernetesResourceMutation, logger *zap.Logger) error {
	convertedData, err := getObjectMetadataFromRuntimeObject(event.Object)
	if err != nil {
		logger.Error("Cannot convert runtime.Object to metav1.ObjectMeta", zap.Error(err))

		return err
	}

	resource := event.Object.GetObjectKind().GroupVersionKind().Kind
	metadataObj := convertMetaObjectToMetadata(ctx, *convertedData, r.clientset, resource)

	// Helper function: type gymnastics + send the KubernetesObjectData on the mutation channel
	mutation := r.streamManager.createMutationObject(metadataObj, event.Type)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case mutationChan <- mutation:
	}

	return nil
}
