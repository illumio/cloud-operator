// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
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
	logger := zap.NewNop().Sugar()

	// Execute the function under test.
	got, _ := convertMetaObjectToMetadata(context.Background(), logger, configMap, "configMap")

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
	logger := zap.NewNop().Sugar()
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

	result, _ := convertMetaObjectToMetadata(context.Background(), logger, objMeta, resource)
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
		expectedIPs    int
		expectedErrMsg string
	}{
		"pod with host IPs": {
			podName:   "test-pod-2",
			namespace: "default",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod-2",
					Namespace: "default",
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:            "busybox",
							Image:           "busybox",
							ImagePullPolicy: v1.PullIfNotPresent,
							Command: []string{
								"sleep",
								"3600",
							},
						},
					},
				},
			},
			expectedIPs:    1,
			expectedErrMsg: "",
		},
		"pod not found": {
			podName:        "nonexistent-pod",
			namespace:      "default",
			pod:            nil,
			expectedIPs:    0,
			expectedErrMsg: "",
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
				assert.Equal(suite.T(), tt.expectedIPs, len(ips))
			}
		})
	}
}

func (suite *ControllerTestSuite) TestFetchResources() {
	// Create dynamic client
	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	clusterConfig, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		suite.T().Fatal("Could not get config", "error", err)
	}
	if err != nil {
		suite.T().Fatal("Error creating cluster config", "error", err)
	}
	dynamicClient, err := dynamic.NewForConfig(clusterConfig)
	if err != nil {
		suite.T().Fatal("Error creating dynamic client", "error", err)
	}

	resourceManager := &ResourceManager{
		dynamicClient: dynamicClient,
		logger:        suite.logger,
	}
	tests := map[string]struct {
		resource  schema.GroupVersionResource
		namespace string
		expectErr bool
	}{
		"valid namespace": {
			resource:  schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
			namespace: "default",
			expectErr: false,
		},
		"invalid namespace": {
			resource:  schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
			namespace: "nonexistent-namespace",
			expectErr: false,
		},
		"empty result": {
			resource:  schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nonexistent-resource"},
			namespace: "default",
			expectErr: true,
		},
	}

	for name, tc := range tests {
		suite.Run(name, func() {
			ctx := context.Background()
			resources, err := resourceManager.FetchResources(ctx, tc.resource, tc.namespace)
			if tc.expectErr {
				assert.Error(suite.T(), err)
			} else {
				assert.NoError(suite.T(), err)
				assert.NotNil(suite.T(), resources)
				if name == "invalid namespace" {
					assert.Empty(suite.T(), resources.Items)
				}
			}
		})
	}
}

func (suite *ControllerTestSuite) TestExtractObjectMetas() {
	tests := map[string]struct {
		inputResources *unstructured.UnstructuredList
		expectedMetas  []metav1.ObjectMeta
		expectedError  bool
		expectedErrMsg string
	}{
		"success": {
			inputResources: &unstructured.UnstructuredList{
				Items: []unstructured.Unstructured{
					{
						Object: map[string]interface{}{
							"apiVersion": "v1",
							"kind":       "Pod",
							"metadata": map[string]interface{}{
								"name":      "test-pod1",
								"namespace": "test-namespace",
								"labels": map[string]string{
									"app": "test-app",
								},
							},
						},
					},
					{
						Object: map[string]interface{}{
							"apiVersion": "v1",
							"kind":       "Pod",
							"metadata": map[string]interface{}{
								"name":      "test-pod2",
								"namespace": "test-namespace",
								"labels": map[string]string{
									"app": "test-app",
								},
							},
						},
					},
				},
			},
			expectedMetas: []metav1.ObjectMeta{
				{Name: "test-pod1", Namespace: "test-namespace"},
				{Name: "test-pod2", Namespace: "test-namespace"},
			},
			expectedError: false,
		},
		"metadata extraction error": {
			inputResources: &unstructured.UnstructuredList{
				Items: []unstructured.Unstructured{
					{
						Object: map[string]interface{}{
							"apiVersion": "v1",
							"kind":       "Pod",
							"metadata_mispelled": map[string]interface{}{
								"name":      "test-pod1",
								"namespace": "test-namespace",
								"labels": map[string]string{
									"app": "test-app",
								},
							},
						},
					},
				},
			},
			expectedMetas:  nil,
			expectedError:  true,
			expectedErrMsg: "could not grab metadata from a resource",
		},
		"empty resource list": {
			inputResources: &unstructured.UnstructuredList{
				Items: []unstructured.Unstructured{},
			},
			expectedMetas: []metav1.ObjectMeta{},
			expectedError: false,
		},
	}

	for name, tc := range tests {
		suite.Run(name, func() {
			resourceManager := ResourceManager{logger: suite.logger}
			// Call the function under test
			objectMetas, err := resourceManager.ExtractObjectMetas(tc.inputResources)

			// Simplify comparison
			for i, obj := range objectMetas {
				objectMetas[i] = metav1.ObjectMeta{
					Name:      obj.Name,
					Namespace: obj.Namespace,
				}
			}
			// Assert the results
			if tc.expectedError {
				assert.Error(suite.T(), err)
				assert.EqualError(suite.T(), err, tc.expectedErrMsg)
			} else {
				assert.NoError(suite.T(), err)
				assert.Equal(suite.T(), tc.expectedMetas, objectMetas)
			}
		})
	}
}
