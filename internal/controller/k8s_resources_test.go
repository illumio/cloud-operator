// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"testing"
	"time"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/protobuf/types/known/timestamppb"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestCacheCurrentEvent(t *testing.T) {
	cache := Cache{cache: make(map[string][32]byte)}
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
	cacheCurrentEvent(objMeta, hashValue, &cache)

	_, ok := cache.cache[string(objMeta.UID)]

	if !ok {
		t.Error("cacheCurrentEvent() did not cache current item")
	}
}

func TestDeleteFromCacheCurrentEvent(t *testing.T) {
	cache := Cache{cache: make(map[string][32]byte)}
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

	cacheCurrentEvent(objMeta, hashValue, &cache)

	// Call function under test.
	deleteFromCacheCurrentEvent(objMeta, &cache)

	_, ok := cache.cache[string(objMeta.UID)]

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
	pod := &v1.Pod{
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

func TestConvertMetaObjectToMetadata(t *testing.T) {
	sampleData := make(map[string]string)
	resource := "test-resource"
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

	expected := &pb.KubernetesObjectData{
		Annotations:       sampleData,
		CreationTimestamp: convertToProtoTimestamp(creationTimestamp),
		Kind:              resource,
		Labels:            sampleData,
		Name:              "test-name",
		Namespace:         "test-namespace",
		ResourceVersion:   "test-version",
		Uid:               "test-uid",
	}

	result := convertMetaObjectToMetadata(objMeta, resource)
	assert.Equal(t, expected, result)
}

func TestConvertToProtoTimestamp(t *testing.T) {
	k8sTime := metav1.Time{Time: time.Now()}
	expected := timestamppb.New(k8sTime.Time)

	result := convertToProtoTimestamp(k8sTime)
	assert.Equal(t, expected, result)
}

func TestConvertHostIPsToStrings(t *testing.T) {
	tests := map[string]struct {
		hostIPs     []v1.HostIP
		expectedIPs []string
	}{
		"empty slice": {
			hostIPs:     []v1.HostIP{},
			expectedIPs: []string{},
		},
		"single IP": {
			hostIPs: []v1.HostIP{
				{IP: "192.168.1.1"},
			},
			expectedIPs: []string{"192.168.1.1"},
		},
		"multiple IPs": {
			hostIPs: []v1.HostIP{
				{IP: "192.168.1.1"},
				{IP: "192.168.1.2"},
				{IP: "192.168.1.3"},
			},
			expectedIPs: []string{"192.168.1.1", "192.168.1.2", "192.168.1.3"},
		},
		"IPs with different formats": {
			hostIPs: []v1.HostIP{
				{IP: "192.168.1.1"},
				{IP: "fe80::1ff:fe23:4567:890a"},
				{IP: "10.0.0.1"},
			},
			expectedIPs: []string{"192.168.1.1", "fe80::1ff:fe23:4567:890a", "10.0.0.1"},
		},
	}

	for name, tt := range tests {
		result := convertHostIPsToStrings(tt.hostIPs)
		assert.Equal(t, tt.expectedIPs, result, "test failed: %s", name)
	}
}

func (suite *ControllerTestSuite) TestGetPodIPAddresses() {
	// Create a development encoder config
	encoderConfig := zap.NewDevelopmentEncoderConfig()
	// Create a JSON encoder
	encoder := zapcore.NewJSONEncoder(encoderConfig)
	// Create syncers for console output
	consoleSyncer := zapcore.AddSync(os.Stdout)
	// Create the core with the atomic level
	core := zapcore.NewTee(
		zapcore.NewCore(encoder, consoleSyncer, zapcore.InfoLevel),
	)
	logger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1)).Sugar()
	logger = logger.With(zap.String("name", "test"))
	tests := map[string]struct {
		podName        string
		namespace      string
		pod            *v1.Pod
		expectedIPs    []v1.HostIP
		expectedErrMsg string
	}{
		"pod with host IPs": {
			podName:   "test-pod",
			namespace: "default",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Status: v1.PodStatus{
					HostIPs: []v1.HostIP{
						{IP: "192.168.1.1"},
						{IP: "192.168.1.2"},
					},
				},
			},
			expectedIPs:    []v1.HostIP{{IP: "192.168.1.1"}, {IP: "192.168.1.2"}},
			expectedErrMsg: "",
		},
		"pod without host IPs": {
			podName:   "test-pod",
			namespace: "default",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Status: v1.PodStatus{
					HostIPs: nil,
				},
			},
			expectedIPs:    []v1.HostIP{},
			expectedErrMsg: "",
		},
		"pod not found": {
			podName:        "nonexistent-pod",
			namespace:      "default",
			pod:            nil,
			expectedIPs:    []v1.HostIP{},
			expectedErrMsg: "Failed to find pod nonexistent-pod in namespace default",
		},
	}
	clientset, err := NewClientSet()
	if err != nil {
		suite.T().Fatal("Failed to get client set " + err.Error())
	}
	for name, tt := range tests {
		suite.Run(name, func() {
			if tt.pod != nil {
				_, err := clientset.CoreV1().Pods(tt.namespace).Create(context.TODO(), tt.pod, metav1.CreateOptions{})
				assert.NoError(suite.T(), err)
			}

			ips, err := getPodIPAddresses(context.TODO(), logger, tt.podName, tt.namespace)
			if tt.expectedErrMsg != "" {
				assert.EqualError(suite.T(), err, tt.expectedErrMsg)
			} else {
				assert.NoError(suite.T(), err)
				assert.Equal(suite.T(), tt.expectedIPs, ips)
			}
		})
	}
}
