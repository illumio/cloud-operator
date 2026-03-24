// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"context"
	"maps"
	"math/rand"
	"slices"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

var resourceList = []string{
	"ciliumclusterwidenetworkpolicies",
	"ciliumnetworkpolicies",
	"cronjobs",
	"customresourcedefinitions",
	"daemonsets",
	"deployments",
	"endpoints",
	"gateways",
	"gatewayclasses",
	"httproutes",
	"ingresses",
	"ingressclasses",
	"jobs",
	"namespaces",
	"networkpolicies",
	"nodes",
	"pods",
	"replicasets",
	"replicationcontrollers",
	"serviceaccounts",
	"services",
	"statefulsets",
}

var resourceAPIGroupMap = make(map[string]string)

// Stream handles the resource stream.
func Stream(ctx context.Context, sm *stream.Manager, logger *zap.Logger, cancel context.CancelFunc, keepalivePeriod time.Duration) error {
	defer cancel()
	defer func() {
		setProcessingResources(false)
	}()

	dynamicClient := sm.K8sClient.GetDynamicClient()
	clientset := sm.K8sClient.GetClientset()

	var err error

	resourceAPIGroupMap, err = buildResourceApiGroupMap(resourceList, clientset, logger)
	if err != nil {
		logger.Error("Failed to build resource api group map", zap.Error(err))

		return err
	}

	setProcessingResources(true)

	err = sm.SendClusterMetadata(ctx, logger)
	if err != nil {
		logger.Error("Failed to send cluster metadata", zap.Error(err))

		return err
	}

	allWatchInfos := make([]watcherInfo, 0, len(resourceAPIGroupMap))

	sharedLimiter := rate.NewLimiter(1, 5)
	resourceManagers := make(map[string]*Watcher)

	for _, resource := range slices.Sorted(maps.Keys(resourceAPIGroupMap)) {
		apiGroup := resourceAPIGroupMap[resource]

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resourceManager := NewWatcher(WatcherConfig{
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

	err = sm.SendResourceSnapshotComplete(logger)
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

		go func(info watcherInfo, manager *Watcher) {
			defer watcherWaitGroup.Done()

			manager.WatchK8sResources(ctx, cancel, info.resourceVersion, mutationChan)
		}(info, resourceManager)
	}

	setProcessingResources(false)

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
			err := sm.SendKeepalive(logger, stream.TypeResources)
			if err != nil {
				return err
			}
		case mutation := <-mutationChan:
			request := &pb.SendKubernetesResourcesRequest{
				Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{
					KubernetesResourceMutation: mutation,
				},
			}

			err := sm.SendToResourceStream(logger, request)
			if err != nil {
				return err
			}

			sm.Stats.IncrementResourceMutations()
		}
	}
}

// ConnectAndStream creates resourceStream client and begins the resource stream.
func ConnectAndStream(sm *stream.Manager, logger *zap.Logger, keepalivePeriod time.Duration) error {
	ctx, cancel := context.WithCancel(context.Background())

	sendKubernetesResourcesStream, err := sm.Client.GrpcClient.SendKubernetesResources(ctx)
	if err != nil {
		cancel()

		return err
	}

	sm.Client.ResourceStream = sendKubernetesResourcesStream

	return Stream(ctx, sm, logger, cancel, keepalivePeriod)
}

// buildResourceApiGroupMap creates a mapping between Kubernetes resources and their API groups.
func buildResourceApiGroupMap(resources []string, clientset kubernetes.Interface, logger *zap.Logger) (map[string]string, error) {
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

type watcherInfo struct {
	resource        string
	apiGroup        string
	resourceVersion string
}

// jitterTime subtracts a random percentage from the base time to introduce jitter.
// maxJitterPct must be in the range [0, 1).
func jitterTime(base time.Duration, maxJitterPct float64) time.Duration {
	jitterPct := rand.Float64() * maxJitterPct //nolint:gosec

	return time.Duration(float64(base) * (1. - jitterPct))
}

func setProcessingResources(processing bool) {
	stream.SetProcessingResources(processing)
}
