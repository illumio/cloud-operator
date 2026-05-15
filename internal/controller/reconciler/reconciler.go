// Copyright 2026 Illumio, Inc. All Rights Reserved.

package reconciler

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller"
	"github.com/illumio/cloud-operator/internal/controller/k8sclient"
	"github.com/illumio/cloud-operator/internal/controller/stream/config/cache"
	"github.com/illumio/cloud-operator/internal/controller/stream/resources"
)

const (
	// FieldManager identifies cloud-operator as the owner of fields in Server-Side Apply.
	FieldManager = "cloud-operator"
)

// Reconciler synchronizes desired state from CloudSecure with actual state in Kubernetes.
type Reconciler struct {
	logger       *zap.Logger
	client       k8sclient.Client
	configCache  *cache.ConfiguredObjectCache
	runtimeCache *cache.RuntimeCache
	resourceInfo map[string]resources.ResourceInfo // discovered API group/version info
}

// NewReconciler creates a new reconciler.
func NewReconciler(
	logger *zap.Logger,
	client k8sclient.Client,
	configCache *cache.ConfiguredObjectCache,
	runtimeCache *cache.RuntimeCache,
) *Reconciler {
	return &Reconciler{
		logger:       logger,
		client:       client,
		configCache:  configCache,
		runtimeCache: runtimeCache,
	}
}

// Start discovers API resources, waits for the config cache to be ready, and starts the reconciliation loop.
// It blocks until the context is cancelled.
func (r *Reconciler) Start(ctx context.Context) {
	// Discover API groups for Cilium resources
	resourceInfo, err := resources.BuildResourceAPIGroupMap(resources.CiliumResources, r.client.GetClientset(), r.logger)
	if err != nil {
		r.logger.Warn("Failed to build resource API group map for Cilium resources", zap.Error(err))
		// Continue with empty map - reconciler will log errors for unknown resources
		resourceInfo = make(map[string]resources.ResourceInfo)
	}

	r.resourceInfo = resourceInfo

	// Wait for both caches to be ready before reconciling
	r.logger.Info("Waiting for config and runtime caches to be ready for reconciliation loop")

	configCh := r.configCache.IsReady()
	runtimeCh := r.runtimeCache.IsReady()

	for configCh != nil || runtimeCh != nil {
		select {
		case <-ctx.Done():
			r.logger.Info("Context cancelled while waiting for caches")

			return
		case <-configCh:
			r.logger.Debug("Config cache is ready")

			configCh = nil
		case <-runtimeCh:
			r.logger.Debug("Runtime cache is ready")

			runtimeCh = nil
		}
	}

	r.logger.Info("Both caches are ready, starting reconciliation loop")

	configChan := r.configCache.ResourceChanged()
	runtimeChan := r.runtimeCache.ResourceChanged()

	for {
		select {
		case <-ctx.Done():
			return
		case id := <-configChan:
			r.handleChange(ctx, id)
		case id := <-runtimeChan:
			r.handleChange(ctx, id)
		}
	}
}

// handleChange does either full or per-object reconciliation based on the ID.
func (r *Reconciler) handleChange(ctx context.Context, id string) {
	if id == cache.SnapshotReplaced {
		if err := r.ReconcileAll(ctx); err != nil {
			r.logger.Error("Full reconciliation failed", zap.Error(err))
		}

		return
	}

	if err := r.ReconcileObject(ctx, id); err != nil {
		r.logger.Error("Object reconciliation failed",
			zap.String("id", id), zap.Error(err))
	}
}

// ReconcileObject reconciles a single object by ID.
// It compares the config cache (desired) with the runtime cache (actual) for that ID
// and applies or deletes as needed.
func (r *Reconciler) ReconcileObject(ctx context.Context, id string) error {
	configObj := r.configCache.Get(id)
	runtimeObj := r.runtimeCache.Get(id)

	switch {
	// In config, not in runtime or different → apply
	case configObj != nil && (runtimeObj == nil || !proto.Equal(configObj, runtimeObj)):
		if err := r.applyObject(ctx, configObj); err != nil {
			return fmt.Errorf("apply %s: %w", id, err)
		}

	// In runtime, not in config → delete
	case configObj == nil && runtimeObj != nil:
		if err := r.deleteObject(ctx, runtimeObj); err != nil {
			return fmt.Errorf("delete %s: %w", id, err)
		}
	}

	return nil
}

// ReconcileAll synchronizes all configured objects from CloudSecure to Kubernetes.
// It applies objects that are new or changed, and deletes objects that are no longer configured.
// Called when the cache receives a full snapshot replacement ("" on the resourceChanged channel).
func (r *Reconciler) ReconcileAll(ctx context.Context) error {
	// Get all configured objects (desired state)
	configuredObjects := r.configCache.Values()

	// Track which CloudSecure IDs we've seen in config (for deletion detection)
	configuredIDs := make(map[string]bool)

	var errs []error

	// Apply configured objects that are new or have changed
	for _, configObj := range configuredObjects {
		configuredIDs[configObj.GetId()] = true

		// Skip apply if runtime state matches desired state
		if runtimeObj := r.runtimeCache.Get(configObj.GetId()); runtimeObj != nil {
			if proto.Equal(configObj, runtimeObj) {
				continue
			}
		}

		if err := r.applyObject(ctx, configObj); err != nil {
			r.logger.Error("Failed to apply configured object",
				zap.String("id", configObj.GetId()),
				zap.String("name", configObj.GetName()),
				zap.Error(err),
			)
			errs = append(errs, fmt.Errorf("apply %s: %w", configObj.GetId(), err))
		}
	}

	// Delete objects that exist in runtime but not in config
	runtimeObjects := r.runtimeCache.Values()
	for _, runtimeObj := range runtimeObjects {
		if !configuredIDs[runtimeObj.GetId()] {
			if err := r.deleteObject(ctx, runtimeObj); err != nil {
				r.logger.Error("Failed to delete object",
					zap.String("id", runtimeObj.GetId()),
					zap.String("name", runtimeObj.GetName()),
					zap.Error(err),
				)
				errs = append(errs, fmt.Errorf("delete %s: %w", runtimeObj.GetId(), err))
			}
		}
	}

	return errors.Join(errs...)
}

// resourceForConfiguredObject returns the plural resource name for a configured object.
func resourceForConfiguredObject(data *pb.ConfiguredKubernetesObjectData) (string, error) {
	var protoType string

	switch data.GetKindSpecific().(type) {
	case *pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy:
		protoType = "CiliumNetworkPolicy"
	case *pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy:
		protoType = "CiliumClusterwideNetworkPolicy"
	case *pb.ConfiguredKubernetesObjectData_CiliumCidrGroup:
		protoType = "CiliumCIDRGroup"
	default:
		return "", fmt.Errorf("unsupported kind_specific type: %T", data.GetKindSpecific())
	}

	info, ok := controller.ResourceKindMap[protoType]
	if !ok {
		return "", fmt.Errorf("unknown proto type: %s", protoType)
	}

	return info.Resource, nil
}

// applyObject applies a single configured object to Kubernetes using Server-Side Apply.
func (r *Reconciler) applyObject(ctx context.Context, configObj *pb.ConfiguredKubernetesObjectData) error {
	// Get resource name from proto type
	resourceName, err := resourceForConfiguredObject(configObj)
	if err != nil {
		return err
	}

	// Get GVR from discovered resource info from desired state cache
	info, ok := r.resourceInfo[resourceName]
	if !ok {
		return fmt.Errorf("resource not discovered: %s", resourceName)
	}

	gvr := schema.GroupVersionResource{Group: info.Group, Version: info.Version, Resource: resourceName}

	// Convert proto → typed Go struct → unstructured map.
	// The dynamic client only accepts *unstructured.Unstructured; using typed Cilium clients
	// would avoid this conversion but adds a large dependency
	obj, err := controller.ToApplyObject(configObj, info.Group, info.Version)
	if err != nil {
		return fmt.Errorf("failed to create runtime object: %w", err)
	}

	unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return fmt.Errorf("failed to convert to unstructured: %w", err)
	}

	desired := &unstructured.Unstructured{Object: unstructuredMap}

	// Use Server-Side Apply
	applied, err := r.client.ApplyResource(ctx, gvr, desired.GetNamespace(), desired, FieldManager)
	if err != nil {
		return fmt.Errorf("failed to apply: %w", err)
	}

	r.logger.Debug("Applied configured object",
		zap.String("id", configObj.GetId()),
		zap.String("name", applied.GetName()),
		zap.String("namespace", applied.GetNamespace()),
		zap.String("kind", applied.GetKind()),
	)

	return nil
}

// deleteObject deletes an object from Kubernetes by deriving the GVR from the configured object.
func (r *Reconciler) deleteObject(ctx context.Context, obj *pb.ConfiguredKubernetesObjectData) error {
	resourceName, err := resourceForConfiguredObject(obj)
	if err != nil {
		return err
	}

	info, ok := r.resourceInfo[resourceName]
	if !ok {
		return fmt.Errorf("resource not discovered: %s", resourceName)
	}

	gvr := schema.GroupVersionResource{Group: info.Group, Version: info.Version, Resource: resourceName}
	namespace := obj.GetNamespace()

	if err := r.client.DeleteResource(ctx, gvr, namespace, obj.GetName()); err != nil {
		return fmt.Errorf("failed to delete: %w", err)
	}

	r.logger.Info("Deleted object no longer in configuration",
		zap.String("id", obj.GetId()),
		zap.String("name", obj.GetName()),
		zap.String("namespace", namespace),
	)

	return nil
}
