// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

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
	"github.com/illumio/cloud-operator/internal/controller"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// WatcherConfig holds the configuration for creating a new Watcher.
type WatcherConfig struct {
	ResourceName  string
	ApiGroup      string
	Clientset     kubernetes.Interface
	BaseLogger    *zap.Logger
	DynamicClient dynamic.Interface
	StreamManager *stream.Manager
	Limiter       *rate.Limiter
}

// Watcher encapsulates components for listing and managing Kubernetes resources.
type Watcher struct {
	resourceName  string
	apiGroup      string
	clientset     kubernetes.Interface
	logger        *zap.Logger
	dynamicClient dynamic.Interface
	streamManager *stream.Manager
	limiter       *rate.Limiter
}

// NewWatcher creates a new Watcher for a specific resource type.
func NewWatcher(config WatcherConfig) *Watcher {
	logger := config.BaseLogger.With(
		zap.String("resource", config.ResourceName),
		zap.String("api_group", config.ApiGroup),
	)

	return &Watcher{
		resourceName:  config.ResourceName,
		apiGroup:      config.ApiGroup,
		clientset:     config.Clientset,
		logger:        logger,
		dynamicClient: config.DynamicClient,
		streamManager: config.StreamManager,
		limiter:       config.Limiter,
	}
}

// WatchK8sResources initiates a watch stream for the specified Kubernetes resource.
func (r *Watcher) WatchK8sResources(ctx context.Context, cancel context.CancelFunc, resourceVersion string, mutationChan chan *pb.KubernetesResourceMutation) {
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
func (r *Watcher) DynamicListResources(ctx context.Context, logger *zap.Logger, apiGroup string) (string, error) {
	objGVR := schema.GroupVersionResource{Group: apiGroup, Version: "v1", Resource: r.resourceName}

	// For Cilium policies, we need the full unstructured object to extract the spec
	if controller.IsCiliumPolicy(removeListSuffix(r.resourceName)) {
		return r.listCiliumResources(ctx, logger, objGVR)
	}

	objs, resourceListVersion, resourceK8sKind, err := r.ListResources(ctx, objGVR, metav1.NamespaceAll)
	if err != nil {
		return "", err
	}

	for _, obj := range objs {
		metadataObj := controller.ConvertMetaObjectToMetadata(ctx, obj, r.clientset, resourceK8sKind)

		err = r.streamManager.SendObjectData(logger, metadataObj)
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

// listCiliumResources handles listing Cilium network policies with full spec conversion.
func (r *Watcher) listCiliumResources(ctx context.Context, logger *zap.Logger, objGVR schema.GroupVersionResource) (string, error) {
	unstructuredResources, err := r.FetchResources(ctx, objGVR, metav1.NamespaceAll)
	if err != nil {
		return "", err
	}

	for i := range unstructuredResources.Items {
		item := &unstructuredResources.Items[i]
		metadataObj := controller.ConvertUnstructuredToCiliumPolicy(item)

		err = r.streamManager.SendObjectData(logger, metadataObj)
		if err != nil {
			r.logger.Error("Cannot send Cilium policy metadata", zap.Error(err))

			return "", err
		}
	}

	r.logger.Debug("Successfully sent Cilium policies", zap.Int("count", len(unstructuredResources.Items)))

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	return unstructuredResources.GetResourceVersion(), nil
}

//nolint:gocognit // function is complex by nature (watch loop)
func (r *Watcher) watchEvents(ctx context.Context, resourceVersion string, mutationChan chan *pb.KubernetesResourceMutation) error {
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

func (r *Watcher) handleWatchError(err error, lastResourceVersion string, logger *zap.Logger) (shouldBreak bool, returnErr error) {
	//nolint:exhaustive // we intentionally only care about specific status reasons
	switch apierrors.ReasonForError(err) {
	case metav1.StatusReasonExpired:
		logger.Warn("Resource version expired, restarting list-and-watch from current state",
			zap.String("expired_resource_version", lastResourceVersion))

		return false, err
	default:
		logger.Error("Error processing watch event", zap.Error(err))

		return true, nil
	}
}

func (r *Watcher) FetchResources(ctx context.Context, resource schema.GroupVersionResource, namespace string) (*unstructured.UnstructuredList, error) {
	unstructuredResources, err := r.dynamicClient.Resource(resource).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		if apierrors.IsForbidden(err) {
			r.logger.Warn("Access forbidden for resource", zap.Stringer("kind", resource), zap.Error(err))

			return nil, fmt.Errorf("access forbidden for resource %s: %w", resource.Resource, err)
		}

		r.logger.Error("Cannot list resource", zap.Stringer("kind", resource), zap.Error(err))

		return nil, err
	}

	return unstructuredResources, nil
}

func (r *Watcher) ExtractObjectMetas(resources *unstructured.UnstructuredList) ([]metav1.ObjectMeta, error) {
	objectMetas := make([]metav1.ObjectMeta, 0, len(resources.Items))
	for _, item := range resources.Items {
		objMeta, err := controller.GetMetadataFromResource(r.logger, item)
		if err != nil {
			r.logger.Error("Cannot get metadata from resource", zap.Error(err))

			return nil, err
		}

		objectMetas = append(objectMetas, *objMeta)
	}

	return objectMetas, nil
}

func (r *Watcher) ListResources(ctx context.Context, resource schema.GroupVersionResource, namespace string) ([]metav1.ObjectMeta, string, string, error) {
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

func removeListSuffix(s string) string {
	if strings.HasSuffix(s, "List") {
		return s[:len(s)-4]
	}

	return s
}

func (r *Watcher) newWatcher(ctx context.Context, resourceVersion string, logger *zap.Logger) (watch.Interface, error) {
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

func (r *Watcher) handleWatchEvent(
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

	return &apierrors.StatusError{ErrStatus: *status}
}

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

func (r *Watcher) processMutation(ctx context.Context, event watch.Event, mutationChan chan *pb.KubernetesResourceMutation) (string, error) {
	resource := event.Object.GetObjectKind().GroupVersionKind().Kind

	var metadataObj *pb.KubernetesObjectData

	// Handle Cilium policies specially to extract full spec
	if controller.IsCiliumPolicy(resource) {
		unstructuredObj, ok := event.Object.(*unstructured.Unstructured)
		if !ok {
			return "", fmt.Errorf("failed to convert event object to unstructured for Cilium policy")
		}

		metadataObj = controller.ConvertUnstructuredToCiliumPolicy(unstructuredObj)
	} else {
		convertedData, err := controller.GetObjectMetadataFromRuntimeObject(event.Object)
		if err != nil {
			return "", fmt.Errorf("failed to convert runtime.Object to metav1.ObjectMeta: %w", err)
		}

		metadataObj = controller.ConvertMetaObjectToMetadata(ctx, *convertedData, r.clientset, resource)
	}

	mutation := r.streamManager.CreateMutationObject(metadataObj, event.Type)

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case mutationChan <- mutation:
	}

	return metadataObj.GetResourceVersion(), nil
}
