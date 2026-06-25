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

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// ResourceStreamSender abstracts the operations for sending resources to CloudSecure.
// Implemented by resourcesClient.
type ResourceStreamSender interface {
	SendObjectData(logger *zap.Logger, metadata *pb.KubernetesObjectData) error
	CreateMutationObject(metadata *pb.KubernetesObjectData, eventType watch.EventType) *pb.KubernetesResourceMutation
}

// ResourceConverter converts an unstructured Kubernetes object into a
// KubernetesObjectData proto. Core and Cilium resources use separate implementations.
type ResourceConverter func(ctx context.Context, obj *unstructured.Unstructured) (*pb.KubernetesObjectData, error)

// RuntimeCacheHandler processes a K8s object for runtime cache population.
// Called by the watcher after converting the K8s object to proto. The handler
// is responsible for filtering (e.g. checking operator-managed labels) and
// updating the runtime cache.
//
// For watch events, eventType is Added/Modified/Deleted and the handler
// inserts or deletes from the runtime cache directly.
//
// For list operations, eventType is empty — the handler accumulates the object
// for a later bulk ReplaceAll without modifying the cache directly.
type RuntimeCacheHandler func(ctx context.Context, eventType watch.EventType, metadata *pb.KubernetesObjectData) error

// MutationCheckpointInterval is the interval for logging mutation checkpoint messages.
const MutationCheckpointInterval = 60 * time.Second

// WatcherConfig holds the configuration for creating a new Watcher.
type WatcherConfig struct {
	ResourceName        string // plural-lowercase (e.g., "pods")
	ApiGroup            string
	ApiVersion          string
	BaseLogger          *zap.Logger
	DynamicClient       dynamic.Interface
	ResourcesClient     ResourceStreamSender // for CloudSecure streaming
	RuntimeCacheHandler RuntimeCacheHandler  // optional: processes events for runtime cache population
	Limiter             *rate.Limiter
	Converter           ResourceConverter
}

// Watcher encapsulates components for listing and managing Kubernetes resources.
type Watcher struct {
	resourceName        string // plural-lowercase (e.g., "pods")
	apiGroup            string
	apiVersion          string
	logger              *zap.Logger
	dynamicClient       dynamic.Interface
	resourcesClient     ResourceStreamSender
	runtimeCacheHandler RuntimeCacheHandler
	limiter             *rate.Limiter
	converter           ResourceConverter
}

// NewWatcher creates a new Watcher for a specific resource type.
func NewWatcher(config WatcherConfig) *Watcher {
	logger := config.BaseLogger.With(
		zap.String("resource", config.ResourceName),
		zap.String("api_group", config.ApiGroup),
		zap.String("api_version", config.ApiVersion),
	)

	return &Watcher{
		resourceName:        config.ResourceName,
		apiGroup:            config.ApiGroup,
		apiVersion:          config.ApiVersion,
		logger:              logger,
		dynamicClient:       config.DynamicClient,
		resourcesClient:     config.ResourcesClient,
		runtimeCacheHandler: config.RuntimeCacheHandler,
		limiter:             config.Limiter,
		converter:           config.Converter,
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

// DynamicListResources lists a specified resource dynamically and sends each item down the current gRPC stream.
// If a RuntimeCacheHandler is set, it is called for each item to perform any cache-related processing.
func (r *Watcher) DynamicListResources(ctx context.Context, logger *zap.Logger) (string, error) {
	unstructuredResources, err := r.FetchResources(ctx, metav1.NamespaceAll)
	if err != nil {
		return "", err
	}

	// Dynamic client may not set GVK on individual list items; derive it from the list GVK.
	listGvk := unstructuredResources.GroupVersionKind()
	itemGvk := schema.GroupVersionKind{
		Group:   listGvk.Group,
		Version: listGvk.Version,
		Kind:    removeListSuffix(listGvk.Kind),
	}

	for i := range unstructuredResources.Items {
		item := &unstructuredResources.Items[i]

		if item.GetKind() == "" {
			item.SetGroupVersionKind(itemGvk)
		}

		metadataObj, err := r.converter(ctx, item)
		if err != nil {
			r.logger.Warn("Skipping resource that failed conversion",
				zap.String("kind", item.GetKind()),
				zap.String("name", item.GetName()),
				zap.String("namespace", item.GetNamespace()),
				zap.String("api_group", item.GroupVersionKind().Group),
				zap.String("api_version", item.GroupVersionKind().Version),
				zap.Error(err))

			continue
		}

		logAWSPolicyIngest(r.logger, "list", metadataObj) // TODO(wonjun): DELETE before PR

		if err := r.resourcesClient.SendObjectData(logger, metadataObj); err != nil {
			r.logger.Error("Cannot send object metadata", zap.Error(err))

			return "", err
		}

		if r.runtimeCacheHandler != nil {
			if err := r.runtimeCacheHandler(ctx, "", metadataObj); err != nil {
				r.logger.Warn("Runtime cache handler error during list event", zap.Error(err))
			}
		}
	}

	r.logger.Debug("Successfully sent resources", zap.Int("count", len(unstructuredResources.Items)))

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

func (r *Watcher) FetchResources(ctx context.Context, namespace string) (*unstructured.UnstructuredList, error) {
	resource := r.gvr()

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

func removeListSuffix(s string) string {
	return strings.TrimSuffix(s, "List")
}

// TODO(wonjun): TEMPORARY debug logging for manual EKS validation. DELETE before PR.
// logAWSPolicyIngest emits a greppable INFO line whenever an AWS VPC CNI policy
// (ClusterNetworkPolicy / ApplicationNetworkPolicy) is ingested and converted.
// Tagged "wonjun" so a manual EKS run can filter the operator log to confirm
// ingest correctness. `source` is "list" (initial sync) or "watch" (live event).
func logAWSPolicyIngest(logger *zap.Logger, source string, obj *pb.KubernetesObjectData) {
	if obj == nil {
		return
	}

	switch obj.GetKindSpecific().(type) {
	case *pb.KubernetesObjectData_AwsClusterNetworkPolicy:
		cnp := obj.GetAwsClusterNetworkPolicy()
		logger.Info("wonjun >>> INGEST ClusterNetworkPolicy",
			zap.String("source", source),
			zap.String("name", obj.GetName()),
			zap.String("api_group", obj.GetApiGroup()),
			zap.String("tier", cnp.GetTier()),
			zap.Int32("priority", cnp.GetPriority()),
			zap.Int("ingress_rules", len(cnp.GetIngress())),
			zap.Int("egress_rules", len(cnp.GetEgress())),
		)
	case *pb.KubernetesObjectData_AwsApplicationNetworkPolicy:
		anp := obj.GetAwsApplicationNetworkPolicy()
		logger.Info("wonjun >>> INGEST ApplicationNetworkPolicy",
			zap.String("source", source),
			zap.String("name", obj.GetName()),
			zap.String("namespace", obj.GetNamespace()),
			zap.String("api_group", obj.GetApiGroup()),
			zap.Bool("ingress_enabled", anp.GetIngress()),
			zap.Bool("egress_enabled", anp.GetEgress()),
			zap.Int("ingress_rules", len(anp.GetIngressRules())),
			zap.Int("egress_rules", len(anp.GetEgressRules())),
		)
	}
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

		resourceVersion, metadataObj, err := r.processMutation(ctx, event, mutationChan)
		if err != nil {
			return "", false, err
		}

		if r.runtimeCacheHandler != nil {
			if err := r.runtimeCacheHandler(ctx, event.Type, metadataObj); err != nil {
				r.logger.Warn("Runtime cache handler error during watch event", zap.Error(err))
			}
		}

		return resourceVersion, true, nil

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

func (r *Watcher) processMutation(ctx context.Context, event watch.Event, mutationChan chan *pb.KubernetesResourceMutation) (string, *pb.KubernetesObjectData, error) {
	if event.Object == nil {
		return "", nil, errors.New("event object is nil")
	}

	unstructuredObj, ok := event.Object.(*unstructured.Unstructured)
	if !ok {
		return "", nil, fmt.Errorf("expected *unstructured.Unstructured, got %T", event.Object)
	}

	metadataObj, err := r.converter(ctx, unstructuredObj)
	if err != nil {
		return "", nil, fmt.Errorf("failed to convert %s %s/%s: %w", unstructuredObj.GetKind(), unstructuredObj.GetNamespace(), unstructuredObj.GetName(), err)
	}

	logAWSPolicyIngest(r.logger, "watch", metadataObj) // TODO(wonjun): DELETE before PR

	mutation := r.resourcesClient.CreateMutationObject(metadataObj, event.Type)

	select {
	case <-ctx.Done():
		return "", nil, ctx.Err()
	case mutationChan <- mutation:
	}

	return metadataObj.GetResourceVersion(), metadataObj, nil
}
