// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
)

// cacheCurrentEvent logs the event's metadata and caches its UID and a hash value into cache.
func cacheCurrentEvent(meta metav1.ObjectMeta, hashedValue [32]byte, cache *Cache) {
	cache.cache[string(meta.UID)] = hashedValue
}

// deleteFromCacheCurrentEvent removes an event's entry from cache using its UID as the key.
func deleteFromCacheCurrentEvent(meta metav1.ObjectMeta, cache *Cache) {
	delete(cache.cache, string(meta.UID))
}

// uniqueEvent checks if an event, identified by the UID in meta, is already present in cache.
// It returns true if the event is unique (not present), false otherwise.
func uniqueEvent(meta metav1.ObjectMeta, cache *Cache, event watch.Event) (bool, error) {
	value := cache.cache[string(meta.UID)]
	hashedValue, err := hashObjectMeta(meta)
	if err != nil {
		return false, err
	}

	if value != hashedValue {
		switch event.Type {
		case watch.Added, watch.Modified:
			cacheCurrentEvent(meta, hashedValue, cache)
		case watch.Deleted:
			deleteFromCacheCurrentEvent(meta, cache)
		}
	}
	return value != hashedValue, nil
}

// hashObjectMeta generates a SHA256 hash of metav1.ObjectMeta's essential fields.
// It returns the hash as a [32]byte and any error encountered during hashing.
func hashObjectMeta(meta metav1.ObjectMeta) ([32]byte, error) {
	// Delete the resourceVersion, causes too many new events that dont impact us.
	meta.ResourceVersion = ""
	// Serialize the ObjectMeta to JSON.
	jsonBytes, err := json.Marshal(meta)
	if err != nil {
		return [32]byte{}, err
	}

	// Compute SHA256 hash of the JSON bytes.
	hash := sha256.Sum256(jsonBytes)
	return hash, nil
}

// convertObjectToMetadata extracts the ObjectMeta from a metav1.Object interface.
func convertObjectToMetadata(obj metav1.Object) metav1.ObjectMeta {
	objMetadata := metav1.ObjectMeta{
		Name:            obj.GetName(),
		Namespace:       obj.GetNamespace(),
		UID:             obj.GetUID(),
		ResourceVersion: obj.GetResourceVersion(),
		Labels:          obj.GetLabels(),
		Annotations:     obj.GetAnnotations(),
	}
	return objMetadata
}

// getObjectMetadataFromRuntimeObject safely extracts metadata from any Kubernetes runtime.Object.
// It returns a pointer to a metav1.ObjectMeta structure if successful, along with any error encountered.
func getObjectMetadataFromRuntimeObject(obj runtime.Object) (*metav1.ObjectMeta, error) {
	objectMeta, err := meta.Accessor(obj)
	if err != nil {
		return nil, err
	}
	convertedObjMeta := convertObjectToMetadata(objectMeta)
	return &convertedObjMeta, nil
}

// getMetadatafromResource extracts the metav1.ObjectMeta from an unstructured.Unstructured resource.
// It utilizes the unstructured's inherent methods to access the metadata directly.
func getMetadatafromResource(logger *zap.SugaredLogger, resource unstructured.Unstructured) (*metav1.ObjectMeta, error) {
	// Convert unstructured object to a map.
	itemMap := resource.Object
	// Extract metadata from map.
	if metadata, found := itemMap["metadata"].(map[string]interface{}); found {
		// Convert the metadata map to JSON and then unmarshal into metav1.ObjectMeta.
		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			logger.Errorw("Error marshalling metadata", "error", err)
			return &metav1.ObjectMeta{}, err
		}
		var objectMeta metav1.ObjectMeta
		if err := json.Unmarshal(metadataJSON, &objectMeta); err != nil {
			logger.Errorw("Error unmarshalling metadata", "error", err)
			return &metav1.ObjectMeta{}, err
		}
		return &objectMeta, err
	} else {
		return &metav1.ObjectMeta{}, errors.New("could not grab metadata from a resource")
	}
}

// convertMetaObjectToMetadata takes a metav1.ObjectMeta and converts it into a proto message object KubernetesMetadata.
func convertMetaObjectToMetadata(ctx context.Context, logger *zap.SugaredLogger, obj metav1.ObjectMeta, resource string) (*pb.KubernetesObjectData, error) {
	objMetadata := &pb.KubernetesObjectData{
		Annotations:       obj.GetAnnotations(),
		CreationTimestamp: convertToProtoTimestamp(obj.CreationTimestamp),
		Kind:              resource,
		Labels:            obj.GetLabels(),
		Name:              obj.GetName(),
		Namespace:         obj.GetNamespace(),
		ResourceVersion:   obj.GetResourceVersion(),
		Uid:               string(obj.GetUID()),
	}
	if resource == "pods" {
		hostIPs, err := getPodIPAddresses(ctx, logger, obj.GetName(), obj.GetNamespace())
		if err != nil {
			logger.Errorw("Cannot grab ip addresses for pod: %s in namespace: %s", obj.GetName(), obj.GetNamespace(), "error", err)
			return &pb.KubernetesObjectData{}, err
		}
		objMetadata.KindSpecific = &pb.KubernetesObjectData_Pod{Pod: &pb.KubernetesPodData{IpAddresses: convertHostIPsToStrings(hostIPs)}}
	}
	return objMetadata, nil
}

// getPodIPAddresses uses a pod name and namespace to grab the hostIP addresses within the podStatus
func getPodIPAddresses(ctx context.Context, logger *zap.SugaredLogger, podName string, namespace string) ([]v1.HostIP, error) {
	clientset, err := NewClientSet()
	if err != nil {
		logger.Errorw("Failed to create clientset", "error", err)
		return []v1.HostIP{}, err
	}
	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		logger.Errorw("Failed to find pod", podName, namespace, "error", err)
		return []v1.HostIP{}, nil
	}
	if pod.Status.HostIPs != nil {
		return pod.Status.HostIPs, nil
	}
	return []v1.HostIP{}, nil
}

// convertHostIPsToStrings converts a slice of v1.HostIP to a slice of strings
func convertHostIPsToStrings(hostIPs []v1.HostIP) []string {
	stringIPs := make([]string, len(hostIPs))
	for i, hostIP := range hostIPs {
		stringIPs[i] = hostIP.IP
	}
	return stringIPs
}

// convertToProtoTimestamp converts a Kubernetes metav1.Time into a Protobuf Timestamp.
func convertToProtoTimestamp(k8sTime metav1.Time) *timestamppb.Timestamp {
	return timestamppb.New(k8sTime.Time)
}

func convertFalcoEventToFlow(event FalcoEvent) (*pb.FalcoFlow, error) {
	layer3Message, err := createLayer3Message(event.SrcIP, event.SrcIP)
	if err != nil {
		return nil, fmt.Errorf("unable to create Layer3 message falco flows: %v", err)
	}

	srcPort, err := strconv.ParseUint(event.SrcPort, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid source port: %v", err)
	}

	dstPort, err := strconv.ParseUint(event.DstPort, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid destination port: %v", err)
	}

	layer4Message, err := CreateLayer4Message(event.Proto, uint32(srcPort), uint32(dstPort), event.IpVersion)
	if err != nil {
		return nil, fmt.Errorf("could not create Layer4 Message for Falco flow %v", err)
	}

	flow := &pb.FalcoFlow{
		Layer3: layer3Message,
		Layer4: layer4Message,
	}

	return flow, nil
}

func createLayer3Message(source string, destination string) (*pb.IP, error) {
	return &pb.IP{Source: source, Destination: destination}, nil
}

// CreateLayer4Message converts event protocol and ports to a Layer4 proto message
func CreateLayer4Message(proto string, srcPort, dstPort uint32, ipVersion string) (*pb.Layer4, error) {
	switch proto {
	case "tcp":
		return &pb.Layer4{
			Protocol: &pb.Layer4_Tcp{
				Tcp: &pb.TCP{
					SourcePort:      srcPort,
					DestinationPort: dstPort,
					Flags:           &pb.TCPFlags{},
				},
			},
		}, nil
	case "udp":
		return &pb.Layer4{
			Protocol: &pb.Layer4_Udp{
				Udp: &pb.UDP{
					SourcePort:      srcPort,
					DestinationPort: dstPort,
				},
			},
		}, nil
	case "icmp":
		if ipVersion == "ipv4" {
			return &pb.Layer4{
				Protocol: &pb.Layer4_Icmpv4{
					Icmpv4: &pb.ICMPv4{},
				},
			}, nil
		} else if ipVersion == "ipv6" {
			return &pb.Layer4{
				Protocol: &pb.Layer4_Icmpv6{
					Icmpv6: &pb.ICMPv6{},
				},
			}, nil
		}
	default:
		return nil, fmt.Errorf("unknown protocol: %s", proto)
	}
	return &pb.Layer4{}, nil
}
