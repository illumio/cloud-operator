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
)

// ResourceStreamSender abstracts the operations for sending resources to CloudSecure.
// Implemented by resourcesClient.
type ResourceStreamSender interface {
	SendObjectData(logger *zap.Logger, metadata *pb.KubernetesObjectData) error
	CreateMutationObject(metadata *pb.KubernetesObjectData, eventType watch.EventType) *pb.KubernetesResourceMutation
}

// MutationCheckpointInterval is the interval for logging mutation checkpoint messages.
const MutationCheckpointInterval = 60 * time.Second

// WatcherConfig holds the configuration for creating a new Watcher.
type WatcherConfig struct {
	ResourceName    string
	ApiGroup        string
	ApiVersion      string
	Clientset       kubernetes.Interface
	BaseLogger      *zap.Logger
	DynamicClient   dynamic.Interface
	ResourcesClient ResourceStreamSender
	Limiter         *rate.Limiter
}

// Watcher encapsulates components for listing and managing Kubernetes resources.
type Watcher struct {
	resourceName    string
	apiGroup        string
	apiVersion      string
	clientset       kubernetes.Interface
	logger          *zap.Logger
	dynamicClient   dynamic.Interface
	resourcesClient ResourceStreamSender
	limiter         *rate.Limiter
}

// NewWatcher creates a new Watcher for a specific resource type.
func NewWatcher(config WatcherConfig) *Watcher {
	logger := config.BaseLogger.With(
		zap.String("resource", config.ResourceName),
		zap.String("api_group", config.ApiGroup),
		zap.String("api_version", config.ApiVersion),
	)

	return &Watcher{
		resourceName:    config.ResourceName,
		apiGroup:        config.ApiGroup,
		apiVersion:      config.ApiVersion,
		clientset:       config.Clientset,
		logger:          logger,
		dynamicClient:   config.DynamicClient,
		resourcesClient: config.ResourcesClient,
		limiter:         config.Limiter,
	}
}

// gvr returns the GroupVersionResource for this watcher.
func (r *Watcher) gvr() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    r.apiGroup,
		Version:  r.apiVersion,
		Resource: r.resourceName,
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
func (r *Watcher) DynamicListResources(ctx context.Context, logger *zap.Logger) (string, error) {
	objGVR := r.gvr()

	// For Cilium policies, we need the full unstructured object to extract the spec.
	// Note: r.resourceName is lowercase plural (e.g., "ciliumnetworkpolicies").
	if controller.IsCiliumPolicy(r.resourceName) {
		return r.listAndSendCiliumResources(ctx, logger, objGVR)
	}

	return r.listAndSendResources(ctx, logger, objGVR)
}

// listAndSendResources lists standard K8s resources and sends them via gRPC.
func (r *Watcher) listAndSendResources(ctx context.Context, logger *zap.Logger, objGVR schema.GroupVersionResource) (string, error) {
	objs, resourceListVersion, resourceK8sKind, err := r.ListResources(ctx, objGVR, metav1.NamespaceAll)
	if err != nil {
		return "", err
	}

	for _, obj := range objs {
		metadataObj := controller.ConvertMetaObjectToMetadata(ctx, obj, r.clientset, resourceK8sKind)

		if err := r.resourcesClient.SendObjectData(logger, metadataObj); err != nil {
			r.logger.Error("Cannot send object metadata", zap.Error(err))

			return "", err
		}
	}

	r.logger.Debug("Successfully sent k8s resources", zap.Int("count", len(objs)))

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	return resourceListVersion, nil
}

// listAndSendCiliumResources lists Cilium network policies and sends them via gRPC.
// Cilium policies require ConvertUnstructuredToCiliumPolicy to extract the full policy spec,
// while regular resources use ConvertMetaObjectToMetadata for just metadata extraction.
func (r *Watcher) listAndSendCiliumResources(ctx context.Context, logger *zap.Logger, objGVR schema.GroupVersionResource) (string, error) {
	unstructuredResources, err := r.FetchResources(ctx, objGVR, metav1.NamespaceAll)
	if err != nil {
		return "", err
	}

	for i := range unstructuredResources.Items {
		item := &unstructuredResources.Items[i]

		metadataObj, err := controller.ConvertUnstructuredToCiliumPolicy(item)
		if err != nil {
			r.logger.Error("Cannot convert Cilium policy",
				zap.String("name", item.GetName()),
				zap.String("namespace", item.GetNamespace()),
				zap.Error(err))

			return "", fmt.Errorf("failed to convert Cilium policy %s/%s: %w", item.GetNamespace(), item.GetName(), err)
		}

		r.logger.Debug("Sending Cilium policy",
			zap.String("name", item.GetName()),
			zap.String("namespace", item.GetNamespace()),
			zap.String("kind", item.GetKind()))

		if err := r.resourcesClient.SendObjectData(logger, metadataObj); err != nil {
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

			case <-time.After(MutationCheckpointInterval):
				logger.Debug("Processed mutations checkpoint", zap.Duration("period", MutationCheckpointInterval), zap.Int("mutation_count", mutationCount))

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
		// Handle expected errors gracefully - these indicate the resource is unavailable
		// but shouldn't cause the entire stream to fail.
		// NotFound: CRD was deleted after initial discovery (e.g., Cilium/Gateway API uninstalled)
		// Forbidden: RBAC doesn't permit access to this resource
		if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) {
			r.logger.Warn("Resource unavailable",
				zap.Stringer("kind", resource),
				zap.String("reason", string(apierrors.ReasonForError(err))),
				zap.Error(err))

			return nil, fmt.Errorf("resource %s unavailable: %w", resource.Resource, err)
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

	objGVR := r.gvr()

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
	if event.Object == nil {
		return "", errors.New("event object is nil")
	}

	// Note: resource is the Kind (PascalCase, e.g., "CiliumNetworkPolicy").
	resource := event.Object.GetObjectKind().GroupVersionKind().Kind

	var metadataObj *pb.KubernetesObjectData

	// Handle Cilium policies specially to extract full spec.
	if controller.IsCiliumPolicy(resource) {
		unstructuredObj, ok := event.Object.(*unstructured.Unstructured)
		if !ok {
			return "", errors.New("failed to convert event object to unstructured for Cilium policy")
		}

		var err error

		metadataObj, err = controller.ConvertUnstructuredToCiliumPolicy(unstructuredObj)
		if err != nil {
			return "", fmt.Errorf("failed to convert Cilium policy: %w", err)
		}
	} else {
		convertedData, err := controller.GetObjectMetadataFromRuntimeObject(event.Object)
		if err != nil {
			return "", fmt.Errorf("failed to convert runtime.Object to metav1.ObjectMeta: %w", err)
		}

		metadataObj = controller.ConvertMetaObjectToMetadata(ctx, *convertedData, r.clientset, resource)
	}

	mutation := r.resourcesClient.CreateMutationObject(metadataObj, event.Type)

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case mutationChan <- mutation:
	}

	return metadataObj.GetResourceVersion(), nil
}
