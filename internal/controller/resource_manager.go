// Copyright 2024 Illumio, Inc. All Rights Reserved.
package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
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
	logger := config.BaseLogger.With(zap.String("resource", config.ResourceName))

	return &ResourceManager{
		resourceName:  config.ResourceName,
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
	// Here intiatate the watch event
	watchOptions := metav1.ListOptions{
		Watch:           true,
		ResourceVersion: resourceVersion,
	}

	err := r.limiter.Wait(ctx)
	if err != nil {
		r.logger.Error("Cannot wait using rate limiter", zap.Error(err))

		return
	}

	err = r.watchEvents(ctx, apiGroup, watchOptions, mutationChan)
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
func (r *ResourceManager) watchEvents(ctx context.Context, apiGroup string, watchOptions metav1.ListOptions, mutationChan chan *pb.KubernetesResourceMutation) error {
	logger := r.logger.With(zap.String("api_group", apiGroup))

	objGVR := schema.GroupVersionResource{Group: apiGroup, Version: "v1", Resource: r.resourceName}

	lastKnownResourceVersion := watchOptions.ResourceVersion

	for {
		// Update watch options with latest resource version
		watchOptions.ResourceVersion = lastKnownResourceVersion

		watcher, err := r.dynamicClient.Resource(objGVR).Namespace(metav1.NamespaceAll).Watch(ctx, watchOptions)
		if err != nil {
			logger.Error("Error setting up watch on resource", zap.Error(err))
			return err
		}

		mutationCount := 0

	watcherLoop:
		for {
			select {
			case <-ctx.Done():
				logger.Debug("Disconnected from CloudSecure", zap.String("reason", "context cancelled"))
				watcher.Stop() // clean up
				return ctx.Err()

			case event, ok := <-watcher.ResultChan():
				if !ok {
					logger.Warn("Watcher channel closed, restarting from resource version",
						zap.String("resource", r.resourceName),
						zap.String("lastResourceVersion", lastKnownResourceVersion))
					watcher.Stop()
					break watcherLoop
				}

				// Extract resource version from ALL event types
				if event.Object != nil {
					if accessor, err := meta.Accessor(event.Object); err == nil {
						if rv := accessor.GetResourceVersion(); rv != "" {
							lastKnownResourceVersion = rv
						}
					}
				}

				switch event.Type {
				case watch.Error:
					err := getErrFromWatchEvent(event)
					logger.Error("Watcher event has returned an error", zap.Error(err))
					watcher.Stop() // ← Added cleanup
					return err

				case watch.Bookmark:
					logger.Debug("Received bookmark",
						zap.String("resourceVersion", lastKnownResourceVersion))
					continue

				case watch.Added, watch.Modified, watch.Deleted:
					logger.Info("Got watch event", zap.String("type", string(event.Type)))

				default:
					if event.Type == "" {
						logger.Warn("Received empty event type, restarting watcher",
							zap.String("resource", r.resourceName),
							zap.String("lastResourceVersion", lastKnownResourceVersion))
						watcher.Stop()
						break watcherLoop
					}
					logger.Debug("Received unknown watch event", zap.String("type", string(event.Type)))
					continue
				}

				// Process mutations (only for Added/Modified/Deleted)
				convertedData, err := getObjectMetadataFromRuntimeObject(event.Object)
				if err != nil {
					logger.Error("Cannot convert runtime.Object to metav1.ObjectMeta", zap.Error(err))
					watcher.Stop() // ← Added cleanup
					return err
				}

				resource := event.Object.GetObjectKind().GroupVersionKind().Kind
				metadataObj, err := convertMetaObjectToMetadata(logger, ctx, *convertedData, r.clientset, resource)
				if err != nil {
					logger.Error("Cannot convert object metadata", zap.Error(err))
					watcher.Stop() // ← Added cleanup
					return err
				}

				mutation, err := r.streamManager.createMutationObject(metadataObj, event.Type)
				if err != nil {
					logger.Error("Cannot send resource mutation", zap.Error(err))
					watcher.Stop() // ← Added cleanup
					return err
				}

				select {
				case <-ctx.Done():
					watcher.Stop() // ← Added cleanup
					return ctx.Err()
				case mutationChan <- mutation:
				}
				mutationCount++

			case <-time.After(60 * time.Second):
				logger.Debug("Current mutation count", zap.Int("mutation_count", mutationCount))
				logger.Debug("Resetting mutation count")
				mutationCount = 0
			}
		}

		// Brief delay before restarting
		time.Sleep(time.Second * 2)
		logger.Info("Restarting watcher",
			zap.String("resource", r.resourceName),
			zap.String("fromResourceVersion", lastKnownResourceVersion))
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
