// Copyright 2026 Illumio, Inc. All Rights Reserved.

package reconciler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
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

	// FullReconcileInterval is the periodic safety net for full reconciliation,
	// catching anything missed due to dropped events or transient failures.
	FullReconcileInterval = 5 * time.Minute
)

// Reconciler synchronizes desired state from CloudSecure with actual state in Kubernetes.
type Reconciler struct {
	logger       *zap.Logger
	client       k8sclient.Client
	configCache  *cache.ConfiguredObjectCache
	runtimeCache *cache.ConfiguredObjectCache
	resourceInfo map[string]resources.ResourceInfo // discovered API group/version info
}

// NewReconciler creates a new reconciler.
func NewReconciler(
	logger *zap.Logger,
	client k8sclient.Client,
	configCache *cache.ConfiguredObjectCache,
	runtimeCache *cache.ConfiguredObjectCache,
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

	// Do an initial full reconciliation before processing incremental changes.
	if err := r.reconcileAll(ctx); err != nil {
		r.logger.Error("Full reconciliation failed", zap.Error(err))
	}

	reconcileTimer := time.NewTimer(FullReconcileInterval)

	for {
		select {
		case <-ctx.Done():
			reconcileTimer.Stop()

			return
		case <-reconcileTimer.C:
			if err := r.reconcileAll(ctx); err != nil {
				r.logger.Error("Full reconciliation failed", zap.Error(err))
			}

			reconcileTimer.Reset(FullReconcileInterval)
		case id := <-r.configCache.ResourceChanged():
			if id == cache.SnapshotReplaced {
				if err := r.reconcileAll(ctx); err != nil {
					r.logger.Error("Full reconciliation failed", zap.Error(err))
				}

				reconcileTimer.Reset(FullReconcileInterval)
			} else {
				if err := r.reconcileObject(ctx, id); err != nil {
					r.logger.Error("Object reconciliation failed", zap.String("id", id), zap.Error(err))
				}
			}
		case id := <-r.runtimeCache.ResourceChanged():
			if id == cache.SnapshotReplaced {
				if err := r.reconcileAll(ctx); err != nil {
					r.logger.Error("Full reconciliation failed", zap.Error(err))
				}

				reconcileTimer.Reset(FullReconcileInterval)
			} else {
				if err := r.reconcileObject(ctx, id); err != nil {
					r.logger.Error("Object reconciliation failed", zap.String("id", id), zap.Error(err))
				}
			}
		}
	}
}

// reconcileObject reconciles a single object by ID.
// It compares the config cache (desired) with the runtime cache (actual) for that ID
// and applies or deletes as needed.
func (r *Reconciler) reconcileObject(ctx context.Context, id string) error {
	configObj := r.configCache.Get(id)
	runtimeObj := r.runtimeCache.Get(id)

	switch {
	// In config, not in runtime or different → apply
	case configObj != nil && (runtimeObj == nil || !r.objectsMatch(configObj, runtimeObj)):
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

// objectsMatch compares a config object with a runtime object, filtering
// runtime annotations to only include keys that CloudSecure explicitly set.
// This prevents additional controllers modifying annotations causing false diffs.
func (r *Reconciler) objectsMatch(configObj, runtimeObj *pb.ConfiguredKubernetesObjectData) bool {
	// If config has no annotations, runtime shouldn't either for comparison purposes.
	// Temporarily set runtime annotations to the intersection before comparing.
	savedAnnotations := runtimeObj.Annotations
	runtimeObj.Annotations = filterAnnotationsByDesired(runtimeObj.GetAnnotations(), configObj.GetAnnotations())

	match := proto.Equal(configObj, runtimeObj)

	runtimeObj.Annotations = savedAnnotations

	return match
}

// filterAnnotationsByDesired returns only runtime annotations whose keys
// exist in the desired (config) annotations.
func filterAnnotationsByDesired(runtimeAnnotations, desiredAnnotations map[string]string) map[string]string {
	if len(desiredAnnotations) == 0 {
		return nil
	}

	if len(runtimeAnnotations) == 0 {
		return nil
	}

	filtered := make(map[string]string, len(desiredAnnotations))

	for key, value := range runtimeAnnotations {
		if _, expected := desiredAnnotations[key]; expected {
			filtered[key] = value
		}
	}

	if len(filtered) == 0 {
		return nil
	}

	return filtered
}

// reconcileAll synchronizes all configured objects from CloudSecure to Kubernetes.
// It reconciles every ID from both caches, applying new/changed objects and deleting
// objects no longer in the config.
func (r *Reconciler) reconcileAll(ctx context.Context) error {
	allIDs := make(map[string]struct{})

	for _, obj := range r.configCache.Values() {
		allIDs[obj.GetId()] = struct{}{}
	}

	for _, obj := range r.runtimeCache.Values() {
		allIDs[obj.GetId()] = struct{}{}
	}

	var errs []error

	for id := range allIDs {
		if err := r.reconcileObject(ctx, id); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// applyObject applies a single configured object to Kubernetes using Server-Side Apply.
func (r *Reconciler) applyObject(ctx context.Context, configObj *pb.ConfiguredKubernetesObjectData) error {
	resourceName, err := controller.ExtractResourceName(configObj)
	if err != nil {
		return err
	}

	info, ok := r.resourceInfo[resourceName]
	if !ok {
		return fmt.Errorf("resource not discovered: %s", resourceName)
	}

	// Convert to unstructured to be able to apply
	desired, _, err := controller.ConvertToApplyObject(configObj, info.Group, info.Version)
	if err != nil {
		return fmt.Errorf("failed to create unstructured object: %w", err)
	}

	gvr := schema.GroupVersionResource{Group: info.Group, Version: info.Version, Resource: resourceName}

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
	resourceName, err := controller.ExtractResourceName(obj)
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
