// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"crypto/sha256"
	"encoding/json"
	"errors"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
)

// cacheCurrentEvent logs the event's metadata and caches its UID and a hash value into cache.
func cacheCurrentEvent(meta metav1.ObjectMeta, hashedValue [32]byte, c CacheManager) {
	c.cache[string(meta.UID)] = hashedValue
}

// deleteFromCacheCurrentEvent removes an event's entry from cache using its UID as the key.
func deleteFromCacheCurrentEvent(meta metav1.ObjectMeta, c CacheManager) {
	delete(c.cache, string(meta.UID))
}

// uniqueEvent checks if an event, identified by the UID in meta, is already present in cache.
// It returns true if the event is unique (not present), false otherwise.
func uniqueEvent(meta metav1.ObjectMeta, c CacheManager, event watch.Event) (bool, error) {
	value := c.cache[string(meta.UID)]
	hashedValue, err := hashObjectMeta(meta)
	if err != nil {
		return false, err
	}

	if value != hashedValue {
		switch event.Type {
		case watch.Added, watch.Modified:
			cacheCurrentEvent(meta, hashedValue, c)
		case watch.Deleted:
			deleteFromCacheCurrentEvent(meta, c)
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
			logger.Error(err, "Error marshalling metadata")
			return &metav1.ObjectMeta{}, err
		}
		var objectMeta metav1.ObjectMeta
		if err := json.Unmarshal(metadataJSON, &objectMeta); err != nil {
			logger.Error(err, "Error unmarshalling metadata")
			return &metav1.ObjectMeta{}, err
		}
		return &objectMeta, err
	} else {
		return &metav1.ObjectMeta{}, errors.New("could not grab metadata from a resource")
	}
}
