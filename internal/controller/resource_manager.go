// Copyright 2024 Illumio, Inc. All Rights Reserved.
package controller

import (
	"context"
	"sync"
	"time"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8scluster/v1"
	"github.com/illumio/cloud-operator/internal/version"
	"go.uber.org/zap"
	"golang.org/x/exp/rand"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
)

// ResourceManager encapsulates components for listing and managing Kubernetes resources.
type ResourceManager struct {
	// Logger provides strucuted logging interface.
	logger *zap.SugaredLogger
	// DynamicClient offers generic Kubernetes API operations.
	dynamicClient dynamic.Interface
	// StreamManager abstracts logic related to starting, using, and managing streams.
	streamManager *streamManager
}

var (
	resourceAPIGroupMap = map[string]string{
		"pods":                      "",
		"nodes":                     "",
		"serviceaccounts":           "",
		"replicationcontrollers":    "",
		"replicasets":               "apps",
		"deployments":               "apps",
		"statefulsets":              "apps",
		"daemonsets":                "apps",
		"ingresses":                 "networking.k8s.io",
		"ingressclasses":            "networking.k8s.io",
		"jobs":                      "batch",
		"cronjobs":                  "batch",
		"services":                  "",
		"networkpolicies":           "networking.k8s.io",
		"customresourcedefinitions": "apiextensions.k8s.io",
		"endpoints":                 "",
		"gateways":                  "gateway.networking.k8s.io",
		"httproutes":                "gateway.networking.k8s.io",
		"gatewayclasses":            "gateway.networking.k8s.io",
	}
)

// sendResourceSnapshotComplete sends a message to indicate that the initial inventory snapshot has been completely streamed into the given stream.
func (rm *ResourceManager) sendResourceSnapshotComplete() error {
	if err := rm.streamManager.instance.resourceStream.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_ResourceSnapshotComplete{}}); err != nil {
		rm.logger.Errorw("Falied to send resource snapshot complete",
			"error", err,
		)
		return err
	}
	return nil
}

// sendClusterMetadata sends a message to indicate current cluster metadata
func (rm *ResourceManager) sendClusterMetadata(ctx context.Context) error {
	clusterUid, err := GetClusterID(ctx, rm.logger)
	if err != nil {
		rm.logger.Errorw("Error getting cluster id", "error", err)
	}
	clientset, err := NewClientSet()
	if err != nil {
		rm.logger.Errorw("Error creating clientset", "error", err)
	}
	kubernetesVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		rm.logger.Errorw("Error getting Kubernetes version", "error", err)
	}
	if err := rm.streamManager.instance.resourceStream.Send(&pb.SendKubernetesResourcesRequest{Request: &pb.SendKubernetesResourcesRequest_ClusterMetadata{ClusterMetadata: &pb.KubernetesClusterMetadata{Uid: clusterUid, KubernetesVersion: kubernetesVersion.String(), OperatorVersion: version.Version()}}}); err != nil {
		rm.logger.Errorw("Failed to send cluster metadata",
			"error", err,
		)
		return err
	}
	return nil
}

// DynamicListAndWatchResources lists and watches the specified resource dynamically, managing context cancellation and synchronization with wait groups.
func (r *ResourceManager) DyanmicListAndWatchResources(ctx context.Context, cancel context.CancelFunc, resource string, allResourcesSnapshotted *sync.WaitGroup, snapshotCompleted *sync.WaitGroup) {
	resourceListVersion, cm, err := r.DynamicListResources(ctx, resource)
	if err != nil {
		allResourcesSnapshotted.Done()
		r.logger.Errorw("Unable to list resources", "error", err)
		cancel()
		return
	}
	allResourcesSnapshotted.Done()
	snapshotCompleted.Wait()
	// Here intiatate the watch event
	watchOptions := metav1.ListOptions{
		Watch:           true,
		ResourceVersion: resourceListVersion,
	}
	// Prevent us from overwhelming K8 api
	randomDuration := time.Duration(rand.Intn(5-1+1)+1) * time.Second
	time.Sleep(randomDuration)
	err = r.watchEvents(ctx, resource, watchOptions, cm)
	if err != nil {
		r.logger.Errorw("Unable to watch events", "error", err)
		cancel()
		return
	}
}

// DynamicListResources lists a specifed resource dynamically and sends down the current gRPC stream.
func (r *ResourceManager) DynamicListResources(ctx context.Context, resource string) (string, CacheManager, error) {
	cm := CacheManager{cache: make(map[string][32]byte)}
	objGVR := schema.GroupVersionResource{Group: resourceAPIGroupMap[resource], Version: "v1", Resource: resource}
	objs, resourceListVersion, err := r.listResources(ctx, objGVR, metav1.NamespaceAll)
	if err != nil {
		return "", cm, err
	}
	for _, obj := range objs {
		metadataObj := convertMetaObjectToMetadata(obj, resource)
		err := sendObjectMetaData(r.streamManager, metadataObj)
		if err != nil {
			r.logger.Errorw("Cannot send object metadata", "error", err)
			return "", cm, err
		}
		hashValue, err := hashObjectMeta(obj)
		if err != nil {
			r.logger.Errorw("Cannot hash current object", "error", err)
			return "", cm, err
		}
		cacheCurrentEvent(obj, hashValue, cm)
	}

	select {
	case <-ctx.Done():
		return "", cm, err
	default:
	}
	return resourceListVersion, cm, nil
}

// watchEvents watches Kubernetes resources and updates cache based on events.
// Any occurring errors are sent through errChanWatch. The watch stops when ctx is cancelled.
func (r *ResourceManager) watchEvents(ctx context.Context, resource string, watchOptions metav1.ListOptions, c CacheManager) error {
	objGVR := schema.GroupVersionResource{Group: resourceAPIGroupMap[resource], Version: "v1", Resource: resource}
	watcher, err := r.dynamicClient.Resource(objGVR).Namespace(metav1.NamespaceAll).Watch(ctx, watchOptions)
	if err != nil {
		r.logger.Errorw("Error setting up watch on resource", "error", err)
		return err
	}
	for event := range watcher.ResultChan() {
		switch event.Type {
		case watch.Error:
			r.logger.Errorw("Watcher event has returned an error", "error", err)
			return err
		case watch.Bookmark:
			continue
		default:
		}
		convertedData, err := getObjectMetadataFromRuntimeObject(event.Object)
		if err != nil {
			r.logger.Errorw("Cannot convert runtime.Object to metav1.ObjectMeta", "error", err)
			return err
		}
		metadataObj := convertMetaObjectToMetadata(*convertedData, resource)

		wasUniqueEvent, err := uniqueEvent(*convertedData, c, event)
		if err != nil {
			r.logger.Errorw("Failed to hash object metadata", "error", err)
			return err
		}
		// This event has been seen before, do not stream the event to CloudSecure.
		if !wasUniqueEvent {
			continue
		}
		err = streamMutationObjectMetaData(r.streamManager, metadataObj, event.Type)
		if err != nil {
			r.logger.Errorw("Cannot send resource mutation", "error", err)
			return err
		}

	}
	return nil
}

// listResources fetches resources of a specified type and namespace, returning their ObjectMeta,
// the last resource version observed, and any error encountered.
func (r *ResourceManager) listResources(ctx context.Context, resource schema.GroupVersionResource, namespace string) ([]metav1.ObjectMeta, string, error) {
	unstructuredResources, err := r.dynamicClient.Resource(resource).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		r.logger.Errorw("Cannot list resource", "error", err, "kind", resource)
		return nil, "", err
	}
	objectMetas := make([]metav1.ObjectMeta, 0, len(unstructuredResources.Items))
	for _, item := range unstructuredResources.Items {
		objMeta, err := getMetadatafromResource(r.logger, item)
		if err != nil {
			r.logger.Errorw("Cannot get Metadata from resource", "error", err)
			return nil, "", err
		}
		objectMetas = append(objectMetas, *objMeta)
	}
	return objectMetas, unstructuredResources.GetResourceVersion(), nil
}
