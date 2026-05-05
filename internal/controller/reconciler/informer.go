// Copyright 2026 Illumio, Inc. All Rights Reserved.

package reconciler

import (
	"context"
	"sync"

	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	k8scache "k8s.io/client-go/tools/cache"

	"github.com/illumio/cloud-operator/internal/controller/k8sclient"
	"github.com/illumio/cloud-operator/internal/controller/stream/config/cache"
)

// Namespaced resources - watched across all namespaces.
var (
	// CiliumNetworkPolicyGVR is the GroupVersionResource for CiliumNetworkPolicy (namespaced).
	CiliumNetworkPolicyGVR = schema.GroupVersionResource{
		Group:    "cilium.io",
		Version:  "v2",
		Resource: "ciliumnetworkpolicies",
	}
)

// Cluster-scoped resources - watched cluster-wide.
var (
	// CiliumClusterwideNetworkPolicyGVR is the GroupVersionResource for CiliumClusterwideNetworkPolicy (cluster-scoped).
	CiliumClusterwideNetworkPolicyGVR = schema.GroupVersionResource{
		Group:    "cilium.io",
		Version:  "v2",
		Resource: "ciliumclusterwidenetworkpolicies",
	}

	// CiliumCIDRGroupGVR is the GroupVersionResource for CiliumCIDRGroup (cluster-scoped).
	CiliumCIDRGroupGVR = schema.GroupVersionResource{
		Group:    "cilium.io",
		Version:  "v2alpha1",
		Resource: "ciliumcidrgroups",
	}
)

// InformerManager manages dynamic informers for Kubernetes resources
// and populates the runtime cache with unstructured Kubernetes objects.
type InformerManager struct {
	logger  *zap.Logger
	client  k8sclient.Client
	cache   *cache.RuntimeCache
	factory dynamicinformer.DynamicSharedInformerFactory

	// stopCh signals informers to stop
	stopCh chan struct{}
	// mutex protects stopCh
	mutex sync.Mutex
}

// NewInformerManager creates a new informer manager.
func NewInformerManager(
	logger *zap.Logger,
	client k8sclient.Client,
	runtimeCache *cache.RuntimeCache,
) *InformerManager {
	// Use NewFilteredDynamicSharedInformerFactory for future extensibility
	// - namespace: metav1.NamespaceAll watches all namespaces (required for namespaced resources)
	// - tweakListOptions: nil for now, can add label selectors later
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
		client.GetDynamicClient(),
		0,                    // No resync - we trust the watch to be reliable
		metav1.NamespaceAll,  // Watch all namespaces
		nil,                  // No list options filter (can add label selector later)
	)

	return &InformerManager{
		logger:  logger,
		client:  client,
		cache:   runtimeCache,
		factory: factory,
		stopCh:  make(chan struct{}),
	}
}

// Start begins watching Kubernetes resources and populating the cache.
// It blocks until the context is cancelled or Stop() is called.
func (m *InformerManager) Start(ctx context.Context) error {
	gvrs := []schema.GroupVersionResource{
		CiliumNetworkPolicyGVR,
		CiliumClusterwideNetworkPolicyGVR,
		CiliumCIDRGroupGVR,
	}

	for _, gvr := range gvrs {
		informer := m.factory.ForResource(gvr).Informer()
		if _, err := informer.AddEventHandler(m.newEventHandler(gvr)); err != nil {
			return err
		}
	}

	// Start all informers
	m.factory.Start(m.stopCh)

	// Wait for initial cache sync
	m.logger.Debug("Waiting for informer cache sync")

	synced := m.factory.WaitForCacheSync(m.stopCh)
	for gvr, ok := range synced {
		if !ok {
			m.logger.Warn("Informer cache sync failed", zap.String("resource", gvr.Resource))
		}
	}

	// Build initial snapshot from all informers and atomically swap
	m.buildInitialSnapshot(gvrs)

	m.logger.Info("Runtime cache sync complete", zap.Int("object_count", m.cache.Len()))

	// Block until stopped
	select {
	case <-ctx.Done():
		m.Stop()

		return ctx.Err()
	case <-m.stopCh:
		return nil
	}
}

// buildInitialSnapshot collects all objects from informers and atomically swaps into cache.
func (m *InformerManager) buildInitialSnapshot(gvrs []schema.GroupVersionResource) {
	snapshot := make(map[string]*unstructured.Unstructured)

	for _, gvr := range gvrs {
		informer := m.factory.ForResource(gvr).Informer()
		for _, item := range informer.GetStore().List() {
			obj, ok := item.(*unstructured.Unstructured)
			if !ok {
				continue
			}

			id := GetCloudSecureID(obj)
			if id == "" {
				// Not managed by CloudSecure, skip
				continue
			}

			snapshot[id] = obj.DeepCopy()
		}
	}

	m.cache.ReplaceAll(snapshot)
}

// Stop signals informers to stop.
func (m *InformerManager) Stop() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	select {
	case <-m.stopCh:
		// Already stopped
	default:
		close(m.stopCh)
	}
}

// newEventHandler creates an event handler that updates the cache.
func (m *InformerManager) newEventHandler(gvr schema.GroupVersionResource) k8scache.ResourceEventHandler {
	return k8scache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			m.handleAdd(gvr, obj)
		},
		UpdateFunc: func(oldObj, newObj any) {
			m.handleUpdate(gvr, newObj)
		},
		DeleteFunc: func(obj any) {
			m.handleDelete(gvr, obj)
		},
	}
}

// handleAdd processes an add event from the informer.
func (m *InformerManager) handleAdd(gvr schema.GroupVersionResource, obj any) {
	unstrObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}

	id := GetCloudSecureID(unstrObj)
	if id == "" {
		// Not managed by CloudSecure, ignore
		return
	}

	// Only update cache after initial sync is complete
	select {
	case <-m.cache.IsReady():
		m.cache.Insert(id, unstrObj.DeepCopy())
		m.logger.Debug("Added runtime object",
			zap.String("id", id),
			zap.String("resource", gvr.Resource),
			zap.String("namespace", unstrObj.GetNamespace()), // empty for cluster-scoped
			zap.String("name", unstrObj.GetName()),
		)
	default:
		// Still in initial sync, buildInitialSnapshot handles it
	}
}

// handleUpdate processes an update event from the informer.
func (m *InformerManager) handleUpdate(gvr schema.GroupVersionResource, obj any) {
	unstrObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}

	id := GetCloudSecureID(unstrObj)
	if id == "" {
		// Not managed by CloudSecure, ignore
		return
	}

	// Only update cache after initial sync is complete
	select {
	case <-m.cache.IsReady():
		m.cache.Insert(id, unstrObj.DeepCopy())
		m.logger.Debug("Updated runtime object",
			zap.String("id", id),
			zap.String("resource", gvr.Resource),
			zap.String("namespace", unstrObj.GetNamespace()), // empty for cluster-scoped
			zap.String("name", unstrObj.GetName()),
		)
	default:
		// Still in initial sync, buildInitialSnapshot handles it
	}
}

// handleDelete processes a delete event from the informer.
func (m *InformerManager) handleDelete(gvr schema.GroupVersionResource, obj any) {
	// Handle DeletedFinalStateUnknown for deleted objects
	if tombstone, ok := obj.(k8scache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}

	unstrObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}

	id := GetCloudSecureID(unstrObj)
	if id == "" {
		// Not managed by CloudSecure, ignore
		return
	}

	// Only update cache after initial sync is complete
	select {
	case <-m.cache.IsReady():
		m.cache.Delete(id)
		m.logger.Debug("Deleted runtime object",
			zap.String("id", id),
			zap.String("resource", gvr.Resource),
			zap.String("namespace", unstrObj.GetNamespace()), // empty for cluster-scoped
			zap.String("name", unstrObj.GetName()),
		)
	default:
		// Still in initial sync, buildInitialSnapshot handles it
	}
}
