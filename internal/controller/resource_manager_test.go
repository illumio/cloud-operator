package controller

import (
	"context"
	"flag"
	"path/filepath"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

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
