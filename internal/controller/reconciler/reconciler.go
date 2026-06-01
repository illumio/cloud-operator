// Copyright 2026 Illumio, Inc. All Rights Reserved.

package reconciler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/k8sclient"
	"github.com/illumio/cloud-operator/internal/controller/stream/config/cache"
	"github.com/illumio/cloud-operator/internal/controller/stream/resources"
	"github.com/illumio/cloud-operator/internal/convert"
)

const (
	// FieldManager identifies cloud-operator as the owner of fields in Server-Side Apply.
	FieldManager = "illumio-cloud-operator"

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

// Run discovers API resources, waits for the config cache to be ready, and runs the reconciliation loop.
// It blocks until the context is cancelled.
func (r *Reconciler) Run(ctx context.Context) {
	var resourceInfo map[string]resources.ResourceInfo
	for attempt := range 5 {
		var err error
		resourceInfo, err = resources.BuildResourceAPIGroupMap(resources.ConfiguredResourceKinds, r.client.GetClientset(), r.logger)
		if err == nil {
			break
		}
		r.logger.Warn("Failed to discover Cilium API groups, retrying",
			zap.Int("attempt", attempt+1),
			zap.Error(err),
		)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	if resourceInfo == nil {
		r.logger.Error("Unable to discover Cilium API groups after retries, reconciler will not start")
		return
	}

	r.resourceInfo = resourceInfo

	if err := r.waitForCaches(ctx); err != nil {
		r.logger.Error("Cache sync failed, reconciler will not start", zap.Error(err))
		return
	}

	r.logger.Info("Both caches are ready, starting reconciliation loop")

	// Do a full initial reconciliation before processing incremental changes
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
			r.processResourceChange(ctx, id, reconcileTimer)
		case id := <-r.runtimeCache.ResourceChanged():
			r.processResourceChange(ctx, id, reconcileTimer)
		}
	}
}

// waitForCaches blocks until both config and runtime caches have received their
// first snapshot. Returns an error if the context is cancelled before both are ready.
func (r *Reconciler) waitForCaches(ctx context.Context) error {
	r.logger.Info("Waiting for config and runtime caches to be ready for reconciliation loop")

	configCacheIsReady := r.configCache.IsReady()
	runtimeCacheIsReady := r.runtimeCache.IsReady()

	for configCacheIsReady != nil || runtimeCacheIsReady != nil {
		select {
		case <-ctx.Done():
			r.logger.Info("Context cancelled while waiting for caches")
			return ctx.Err()
		case <-configCacheIsReady:
			r.logger.Debug("Config cache is ready")

			configCacheIsReady = nil
		case <-runtimeCacheIsReady:
			r.logger.Debug("Runtime cache is ready")

			runtimeCacheIsReady = nil
		}
	}

	return nil
}

// processResourceChange handles a single resource change by reconciling the
// affected object, or performing a full reconciliation if the entire snapshot was replaced.
func (r *Reconciler) processResourceChange(ctx context.Context, id string, reconcileTimer *time.Timer) {
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

// reconcileObject reconciles a single object by ID.
// If the object exists in the config cache and differs from the runtime cache, it is applied via SSA.
// If the object exists only in the runtime cache (not in config), it is deleted.
func (r *Reconciler) reconcileObject(ctx context.Context, id string) error {
	configObj := r.configCache.Get(id)
	runtimeObj := r.runtimeCache.Get(id)

	switch {
	// In config and differs from runtime (or not yet applied) → apply
	case configObj != nil && !proto.Equal(configObj, runtimeObj):
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

// reconcileAll synchronizes all configured objects from CloudSecure to Kubernetes.
// It reconciles every ID from both caches, applying new/changed objects and deleting
// objects no longer in the config.
func (r *Reconciler) reconcileAll(ctx context.Context) error {
	allIDs := make(map[string]struct{}, max(r.configCache.Len(), r.runtimeCache.Len()))

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
// SSA field ownership ensures that annotations managed by cloud-operator are authoritative:
// omitted annotations are removed, and annotations owned by other field managers are preserved.
func (r *Reconciler) applyObject(ctx context.Context, configObj *pb.ConfiguredKubernetesObjectData) error {
	resourceName, err := convert.ExtractResourceName(configObj)
	if err != nil {
		return err
	}

	info, ok := r.resourceInfo[resourceName]
	if !ok {
		return fmt.Errorf("resource not discovered: %s", resourceName)
	}

	// Convert to unstructured to be able to apply
	desired, _, err := convert.ConvertToApplyObject(configObj, info.Group, info.Version)
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
	resourceName, err := convert.ExtractResourceName(obj)
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
		if apierrors.IsNotFound(err) {
			r.logger.Debug("Object already deleted", zap.String("name", obj.GetName()))
			return nil
		}
		return fmt.Errorf("failed to delete: %w", err)
	}

	r.logger.Debug("Deleted object no longer in configuration",
		zap.String("id", obj.GetId()),
		zap.String("name", obj.GetName()),
		zap.String("namespace", namespace),
	)

	return nil
}
