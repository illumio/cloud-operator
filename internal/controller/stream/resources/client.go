// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/watch"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/auth"
	"github.com/illumio/cloud-operator/internal/controller/k8sclient"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/version"
)

// Verify resourcesClient implements stream.StreamClient.
var _ stream.StreamClient = (*resourcesClient)(nil)

// KubernetesResourcesStream abstracts the SendKubernetesResources gRPC stream.
type KubernetesResourcesStream interface {
	Send(req *pb.SendKubernetesResourcesRequest) error
	Recv() (*pb.SendKubernetesResourcesResponse, error)
}

// resourcesClient implements stream.StreamClient for the resources stream.
type resourcesClient struct {
	grpcStream    KubernetesResourcesStream
	logger        *zap.Logger
	k8sClient     k8sclient.Client
	stats         *stream.Stats
	flowCollector pb.FlowCollector
	clusterName   string // Optional: cluster name for self-managed clusters

	mutex  sync.RWMutex
	closed bool
}

// Run streams Kubernetes resources to CloudSecure.
func (c *resourcesClient) Run(ctx context.Context) error {
	defer func() {
		stream.SetProcessingResources(false)
	}()

	dynamicClient := c.k8sClient.GetDynamicClient()
	clientset := c.k8sClient.GetClientset()

	resourceAPIGroupMap, err := buildResourceApiGroupMap(resourceList, clientset, c.logger)
	if err != nil {
		c.logger.Error("Failed to build resource api group map", zap.Error(err))

		return err
	}

	stream.SetProcessingResources(true)

	err = c.sendClusterMetadata(ctx)
	if err != nil {
		c.logger.Error("Failed to send cluster metadata", zap.Error(err))

		return err
	}

	allWatchInfos := make([]watcherInfo, 0, len(resourceAPIGroupMap))
	sharedLimiter := rate.NewLimiter(1, 5)
	resourceManagers := make(map[string]*Watcher)

	for _, resource := range slices.Sorted(maps.Keys(resourceAPIGroupMap)) {
		resourceInfo := resourceAPIGroupMap[resource]

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resourceManager := NewWatcher(WatcherConfig{
			ResourceName:    resource,
			ApiGroup:        resourceInfo.Group,
			ApiVersion:      resourceInfo.Version,
			Clientset:       clientset,
			BaseLogger:      c.logger,
			DynamicClient:   dynamicClient,
			ResourcesClient: c,
			Limiter:         sharedLimiter,
		})
		resourceManagers[resource] = resourceManager

		resourceVersion, err := resourceManager.DynamicListResources(ctx, resourceManager.logger)
		if err != nil {
			// Skip resources that are unavailable due to RBAC or missing CRDs.
			// NotFound can occur when a CRD is deleted after discovery but before listing.
			if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) {
				c.logger.Warn("Skipping unavailable resource",
					zap.String("kind", resource),
					zap.String("api_group", resourceInfo.Group),
					zap.String("reason", string(apierrors.ReasonForError(err))),
					zap.Error(err))

				continue
			}

			return err
		}

		allWatchInfos = append(allWatchInfos, watcherInfo{
			resource:        resource,
			apiGroup:        resourceInfo.Group,
			apiVersion:      resourceInfo.Version,
			resourceVersion: resourceVersion,
		})
	}

	err = c.sendResourceSnapshotComplete()
	if err != nil {
		c.logger.Error("Failed to send snapshot complete", zap.Error(err))

		return err
	}

	c.logger.Info("Successfully sent resource snapshot")

	mutationChan := make(chan *pb.KubernetesResourceMutation)
	watcherWaitGroup := sync.WaitGroup{}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, info := range allWatchInfos {
		resourceManager := resourceManagers[info.resource]

		watcherWaitGroup.Add(1)

		go func(info watcherInfo, manager *Watcher) {
			defer watcherWaitGroup.Done()

			manager.WatchK8sResources(ctx, cancel, info.resourceVersion, mutationChan)
		}(info, resourceManager)
	}

	stream.SetProcessingResources(false)

	go func() {
		watcherWaitGroup.Wait()
		close(mutationChan)
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case mutation, ok := <-mutationChan:
			if !ok {
				return nil
			}

			request := &pb.SendKubernetesResourcesRequest{
				Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{
					KubernetesResourceMutation: mutation,
				},
			}

			err := c.SendToStream(request)
			if err != nil {
				return err
			}

			c.stats.IncrementResourceMutations()
		}
	}
}

// SendKeepalive sends a keepalive message on the resources stream.
func (c *resourcesClient) SendKeepalive(_ context.Context) error {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if c.closed {
		return errors.New("stream closed")
	}

	err := c.grpcStream.Send(&pb.SendKubernetesResourcesRequest{
		Request: &pb.SendKubernetesResourcesRequest_Keepalive{
			Keepalive: &pb.Keepalive{},
		},
	})
	if err != nil {
		c.logger.Error("Failed to send keepalive on resources stream", zap.Error(err))

		return err
	}

	return nil
}

// Close marks the client as closed.
func (c *resourcesClient) Close() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.closed = true

	return nil
}

// SendToStream sends a request to the resources stream.
func (c *resourcesClient) SendToStream(request *pb.SendKubernetesResourcesRequest) error {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if c.closed {
		return errors.New("stream closed")
	}

	if err := c.grpcStream.Send(request); err != nil {
		c.logger.Error("Failed to send request", zap.Stringer("request", request), zap.Error(err))

		return err
	}

	return nil
}

// SendObjectData sends a KubernetesObjectData to CloudSecure.
func (c *resourcesClient) SendObjectData(_ *zap.Logger, metadata *pb.KubernetesObjectData) error {
	request := &pb.SendKubernetesResourcesRequest{
		Request: &pb.SendKubernetesResourcesRequest_ResourceData{
			ResourceData: metadata,
		},
	}

	return c.SendToStream(request)
}

// CreateMutationObject converts a KubernetesObjectData into a KubernetesResourceMutation.
func (c *resourcesClient) CreateMutationObject(metadata *pb.KubernetesObjectData, eventType watch.EventType) *pb.KubernetesResourceMutation {
	var mutation *pb.KubernetesResourceMutation

	switch eventType {
	case watch.Added:
		mutation = &pb.KubernetesResourceMutation{
			Mutation: &pb.KubernetesResourceMutation_CreateResource{
				CreateResource: metadata,
			},
		}
	case watch.Deleted:
		mutation = &pb.KubernetesResourceMutation{
			Mutation: &pb.KubernetesResourceMutation_DeleteResource{
				DeleteResource: metadata,
			},
		}
	case watch.Modified:
		mutation = &pb.KubernetesResourceMutation{
			Mutation: &pb.KubernetesResourceMutation_UpdateResource{
				UpdateResource: metadata,
			},
		}
	case watch.Bookmark:
	case watch.Error:
	}

	return mutation
}

// sendClusterMetadata sends cluster metadata to CloudSecure.
func (c *resourcesClient) sendClusterMetadata(ctx context.Context) error {
	clientset := c.k8sClient.GetClientset()

	clusterUid, err := auth.GetClusterID(ctx, c.logger, clientset)
	if err != nil {
		c.logger.Error("Error getting cluster id", zap.Error(err))

		return err
	}

	kubernetesVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		c.logger.Error("Error getting Kubernetes version", zap.Error(err))

		return err
	}

	metadata := &pb.KubernetesClusterMetadata{
		Uid:               clusterUid,
		KubernetesVersion: kubernetesVersion.String(),
		OperatorVersion:   version.Version(),
		FlowCollector:     c.flowCollector,
	}

	if c.clusterName != "" {
		metadata.ClusterName = &c.clusterName
	}

	request := &pb.SendKubernetesResourcesRequest{
		Request: &pb.SendKubernetesResourcesRequest_ClusterMetadata{
			ClusterMetadata: metadata,
		},
	}

	return c.SendToStream(request)
}

// sendResourceSnapshotComplete sends a message indicating the initial snapshot is complete.
func (c *resourcesClient) sendResourceSnapshotComplete() error {
	request := &pb.SendKubernetesResourcesRequest{
		Request: &pb.SendKubernetesResourcesRequest_ResourceSnapshotComplete{},
	}

	return c.SendToStream(request)
}
