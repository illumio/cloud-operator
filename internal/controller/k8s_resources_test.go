// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestCacheCurrentEvent(t *testing.T) {
	c := Cache{cache: make(map[string][32]byte)}
	sampleData := make(map[string]string)
	creationTimestamp := metav1.Time{Time: time.Now()}
	objMeta := metav1.ObjectMeta{
		Annotations:       sampleData,
		CreationTimestamp: creationTimestamp,
		Labels:            sampleData,
		Name:              "test-name",
		Namespace:         "test-namespace",
		ResourceVersion:   "test-version",
		UID:               "test-uid",
	}
	hashValue, _ := hashObjectMeta(objMeta)

	// Call function under test.
	cacheCurrentEvent(objMeta, hashValue, c)

	_, ok := c.cache[string(objMeta.UID)]

	if !ok {
		t.Error("cacheCurrentEvent() did not cache current item")
	}
}

func TestDeleteFromCacheCurrentEvent(t *testing.T) {
	c := Cache{cache: make(map[string][32]byte)}
	sampleData := make(map[string]string)
	creationTimestamp := metav1.Time{Time: time.Now()}
	objMeta := metav1.ObjectMeta{
		Annotations:       sampleData,
		CreationTimestamp: creationTimestamp,
		Labels:            sampleData,
		Name:              "test-name",
		Namespace:         "test-namespace",
		ResourceVersion:   "test-version",
		UID:               "test-uid",
	}
	hashValue, _ := hashObjectMeta(objMeta)

	cacheCurrentEvent(objMeta, hashValue, c)

	// Call function under test.
	deleteFromCacheCurrentEvent(objMeta, c)

	_, ok := c.cache[string(objMeta.UID)]

	if ok {
		t.Error("deleteFromCacheCurrentEvent() did not delete current obj from cache")
	}
}
func TestHashObjectMeta(t *testing.T) {
	unMarshalJSON, _ := json.Marshal(metav1.ObjectMeta{Name: "test-name", UID: "test-uid"})
	hash := sha256.Sum256(unMarshalJSON)
	// Define test cases
	tests := []struct {
		name     string
		meta     metav1.ObjectMeta
		expected [32]byte
	}{
		{
			name: "standard object meta",
			meta: metav1.ObjectMeta{Name: "test-name", UID: "test-uid"},
			// Provide the exact expected hash for the concatenated Name and UID.
			expected: hash,
		},
		// Add more test cases as necessary, including those with empty fields.
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hash, err := hashObjectMeta(tc.meta)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if hash != tc.expected {
				t.Errorf("Hash did not match expected value for %s. got: %x, want: %x", tc.name, hash, tc.expected)
			}
		})
	}
}

func TestConvertObjectToMetadata(t *testing.T) {
	// Setup a mock object, e.g., a ConfigMap with predefined metadata
	configMap := metav1.ObjectMeta{
		Name:            "test-pod",
		Namespace:       "test-namespace",
		UID:             "test-uid",
		ResourceVersion: "test-version",
	}

	// Execute the function under test.
	got := convertMetaObjectToMetadata(configMap, "configMap")

	// Define what you expect to get.
	want := metav1.ObjectMeta{
		Name:            "test-pod",
		Namespace:       "test-namespace",
		UID:             "test-uid",
		ResourceVersion: "test-version",
	}

	// Compare the result with the expected outcome.
	if got.Name != want.Name || got.Namespace != want.Namespace || string(got.GetUid()) != string(want.UID) || got.ResourceVersion != want.ResourceVersion {
		t.Errorf("convertObjectToMetadata() = %#v, want %#v", got, want)
	}
}

func TestGetObjectMetadataFromRuntimeObject(t *testing.T) {
	// A successful case with a valid Kubernetes object.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			Labels:    map[string]string{"app": "testing"},
		},
	}

	metaData, err := getObjectMetadataFromRuntimeObject(pod)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if metaData.Name != "test-pod" {
		t.Errorf("Expected Name to be 'test-pod', got '%s'", metaData.Name)
	}
}

func TestGetMetadataFromResource(t *testing.T) {
	// Create a no-op logger.
	logger := zap.NewNop().Sugar()

	// Create an `unstructured.Unstructured` object with metadata.
	resource := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]interface{}{
				"name":      "test-pod",
				"namespace": "test-namespace",
				"labels": map[string]string{
					"app": "test-app",
				},
			},
		},
	}

	// Call the function under test.
	metadata, _ := getMetadatafromResource(logger, resource)

	// Validate the results.
	if metadata.Name != "test-pod" || metadata.Namespace != "test-namespace" || metadata.Labels["app"] != "test-app" {
		t.Errorf("Incorrect metadata extracted: %+v", metadata)
	}
}
