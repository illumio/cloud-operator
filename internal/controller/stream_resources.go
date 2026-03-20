// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

// ServerIsHealthy checks if a deadlock has occurred within the threaded resource listing process.
func ServerIsHealthy() bool {
	dd.mutex.RLock()
	defer dd.mutex.RUnlock()

	if dd.processingResources && time.Since(dd.timeStarted) > 5*time.Minute {
		return false
	}

	return true
}

// disableSubsystemCausingError mutates the `streamManager`
// receiver by inspecting the error. Thanks to HandleTLSHandshakeError
// we have a nicely typed error. If we recognize the specific error,
// then disable JUST the subsystem that caused it. If we do not
// recognize the error, then just give up entirely on exporting Cilium flows.
func (sm *streamManager) disableSubsystemCausingError(err error, logger *zap.Logger) {
	switch {
	case errors.Is(err, tls.ErrTLSALPNHandshakeFailed):
		logger.Info("Disabling ALPN for Hubble Relay connection; will retry connecting")

		sm.streamClient.tlsAuthProperties.DisableALPN = true
	case errors.Is(err, tls.ErrNoTLSHandshakeFailed):
		logger.Info("Disabling TLS for Hubble Relay connection; will retry connecting")

		sm.streamClient.tlsAuthProperties.DisableTLS = true
	default:
		logger.Warn("Disabling network flow collection from Hubble Relay due to unrecoverable error", zap.Error(err))

		sm.streamClient.disableNetworkFlowsCilium = true
	}
}

// buildResourceApiGroupMap creates a mapping between Kubernetes resources and their corresponding API groups.
func (sm *streamManager) buildResourceApiGroupMap(resources []string, clientset kubernetes.Interface, logger *zap.Logger) (map[string]string, error) {
	resourceAPIGroupMap := make(map[string]string)

	resourceSet := make(map[string]struct{})
	for _, resource := range resources {
		resourceSet[resource] = struct{}{}
	}

	discoveryClient := clientset.Discovery()

	apiGroups, err := discoveryClient.ServerGroups()
	if err != nil {
		logger.Error("Error fetching API groups", zap.Error(err))

		return resourceAPIGroupMap, err
	}

	for _, group := range apiGroups.Groups {
		for _, version := range group.Versions {
			resourceList, err := discoveryClient.ServerResourcesForGroupVersion(version.GroupVersion)
			if err != nil {
				if apierrors.IsForbidden(err) {
					continue
				} else {
					return nil, err
				}
			}

			for _, resource := range resourceList.APIResources {
				if _, exists := resourceSet[resource.Name]; exists {
					if group.Name == "metrics.k8s.io" {
						logger.Info("Skipping this as it causes issues with discovery",
							zap.String("group", group.Name),
							zap.String("resource", resource.Name),
						)

						continue
					}

					resourceAPIGroupMap[resource.Name] = group.Name
				}
			}
		}
	}

	return resourceAPIGroupMap, nil
}

// StreamResources handles the resource stream.
func (sm *streamManager) StreamResources(ctx context.Context, logger *zap.Logger, cancel context.CancelFunc, keepalivePeriod time.Duration) error {
	defer cancel()
	defer func() {
		dd.processingResources = false
	}()

	dynamicClient := sm.k8sClient.GetDynamicClient()
	clientset := sm.k8sClient.GetClientset()

	var err error

	resourceAPIGroupMap, err = sm.buildResourceApiGroupMap(resources, clientset, logger)
	if err != nil {
		logger.Error("Failed to build resource api group map", zap.Error(err))

		return err
	}

	dd.mutex.Lock()
	dd.timeStarted = time.Now()
	dd.processingResources = true
	dd.mutex.Unlock()

	err = sm.sendClusterMetadata(ctx, logger)
	if err != nil {
		logger.Error("Failed to send cluster metadata", zap.Error(err))

		return err
	}

	allWatchInfos := make([]watcherInfo, 0, len(resourceAPIGroupMap))

	sharedLimiter := rate.NewLimiter(1, 5)
	resourceManagers := make(map[string]*ResourceManager)

	for _, resource := range slices.Sorted(maps.Keys(resourceAPIGroupMap)) {
		apiGroup := resourceAPIGroupMap[resource]

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resourceManager := NewResourceManager(ResourceManagerConfig{
			ResourceName:  resource,
			ApiGroup:      apiGroup,
			Clientset:     clientset,
			BaseLogger:    logger,
			DynamicClient: dynamicClient,
			StreamManager: sm,
			Limiter:       sharedLimiter,
		})
		resourceManagers[resource] = resourceManager

		resourceVersion, err := resourceManager.DynamicListResources(ctx, resourceManager.logger, apiGroup)
		if err != nil {
			if apierrors.IsForbidden(err) {
				logger.Warn("Access forbidden for resource", zap.String("kind", resource), zap.String("api_group", apiGroup), zap.Error(err))

				continue
			}

			return err
		}

		allWatchInfos = append(allWatchInfos, watcherInfo{
			resource:        resource,
			apiGroup:        apiGroup,
			resourceVersion: resourceVersion,
		})
	}

	err = sm.sendResourceSnapshotComplete(logger)
	if err != nil {
		logger.Error("Failed to send snapshot complete", zap.Error(err))

		return err
	}

	logger.Info("Successfully sent resource snapshot")

	mutationChan := make(chan *pb.KubernetesResourceMutation)
	watcherWaitGroup := sync.WaitGroup{}

	for _, info := range allWatchInfos {
		resourceManager := resourceManagers[info.resource]

		watcherWaitGroup.Add(1)

		go func(info watcherInfo, manager *ResourceManager) {
			defer watcherWaitGroup.Done()

			manager.WatchK8sResources(ctx, cancel, info.resourceVersion, mutationChan)
		}(info, resourceManager)
	}

	dd.mutex.Lock()
	dd.processingResources = false
	dd.mutex.Unlock()

	ticker := time.NewTicker(jitterTime(keepalivePeriod, 0.10))
	defer ticker.Stop()

	go func() {
		watcherWaitGroup.Wait()
		close(mutationChan)
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			err := sm.sendKeepalive(logger, STREAM_RESOURCES)
			if err != nil {
				return err
			}
		case mutation := <-mutationChan:
			request := &pb.SendKubernetesResourcesRequest{
				Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{
					KubernetesResourceMutation: mutation,
				},
			}

			err := sm.sendToResourceStream(logger, request)
			if err != nil {
				return err
			}

			sm.stats.IncrementResourceMutations()
		}
	}
}

// connectAndStreamResources creates resourceStream client and begins the resource stream.
func (sm *streamManager) connectAndStreamResources(logger *zap.Logger, keepalivePeriod time.Duration) error {
	ctx, cancel := context.WithCancel(context.Background())

	sendKubernetesResourcesStream, err := sm.streamClient.client.SendKubernetesResources(ctx)
	if err != nil {
		cancel()

		return err
	}

	sm.streamClient.resourceStream = sendKubernetesResourcesStream

	return sm.StreamResources(ctx, logger, cancel, keepalivePeriod)
}
