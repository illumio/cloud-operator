// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"errors"
	"flag"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// ptrBool is a helper function to create pointers to bool values (used in metav1.OwnerReference)
// Used to simulate the nil behavior of the optional fields
func ptrBool(b bool) *bool {
	return &b
}

// ptrString is a helper function to create pointers to string values
// Used to simulate the nil behavior of the optional fields
func ptrString(s string) *string {
	return &s
}

// int32ToUint32 converts *int32 to *uint32
func ptrInt32ToUint32(i *int32) *uint32 {
	if i == nil {
		return nil
	}
	val := uint32(*i)
	return &val
}

// ptrUint32 is a helper function to create pointers to int32 values
// Used to simulate the nil behavior of the optional fields
func ptrUint32(n uint32) *uint32 {
	return &n
}
func (suite *ControllerTestSuite) TestConvertObjectToMetadata() {
	// Setup a mock object, e.g., a ConfigMap with predefined metadata
	configMap := metav1.ObjectMeta{
		Name:            "test-pod",
		Namespace:       "test-namespace",
		UID:             "test-uid",
		ResourceVersion: "test-version",
	}
	logger := zap.NewNop()
	clientset, err := NewClientSet()
	if err != nil {
		logger.Error("Failed to create clientset", zap.Error(err))
		suite.T().Error("could not create clientset")
	}
	// Execute the function under test.
	got, _ := convertMetaObjectToMetadata(logger, context.Background(), configMap, clientset, "configMap")

	// Define what you expect to get.
	want := metav1.ObjectMeta{
		Name:            "test-pod",
		Namespace:       "test-namespace",
		UID:             "test-uid",
		ResourceVersion: "test-version",
	}

	// Compare the result with the expected outcome.
	if got.Name != want.Name || got.Namespace != want.Namespace || string(got.GetUid()) != string(want.UID) || got.ResourceVersion != want.ResourceVersion {
		suite.T().Errorf("convertObjectToMetadata() = %#v, want %#v", got, want)
	}
}

func TestRemoveListSuffix(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedOutput string
	}{
		"empty string": {
			input:          "",
			expectedOutput: "",
		},
		"no List suffix": {
			input:          "Pod",
			expectedOutput: "Pod",
		},
		"with List suffix": {
			input:          "PodList",
			expectedOutput: "Pod",
		},
		"multiple capitalizations": {
			input:          "StatefulSetList",
			expectedOutput: "StatefulSet",
		},
		"List suffix at the end": {
			input:          "ReplicaSetList",
			expectedOutput: "ReplicaSet",
		},
		"string with List at the start": {
			input:          "ListPod",
			expectedOutput: "ListPod", // Since "List" is at the start, it shouldn't be removed
		},
		"string with embedded List": {
			input:          "MyListPod",
			expectedOutput: "MyListPod", // Should not remove the "List" that appears inside the string
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := removeListSuffix(tt.input)

			assert.Equal(t, tt.expectedOutput, result, "test failed: %s", name)
		})
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
	logger := zap.NewNop()

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

func (suite *ControllerTestSuite) TestConvertMetaObjectToMetadata() {
	logger := zap.NewNop()

	clientset, err := NewClientSet()
	if err != nil {
		logger.Error("Failed to create clientset", zap.Error(err))
		suite.T().Fatalf("could not create clientset: %v", err)
	}

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

	// Ensure proper error handling
	result, err := convertMetaObjectToMetadata(logger, context.Background(), objMeta, clientset, resource)
	if err != nil {
		suite.T().Fatalf("Error converting MetaObject to Metadata: %v", err)
	}

	assert.Equal(suite.T(), expected, result)
}

func (suite *ControllerTestSuite) TestConvertOwnerReferences() {
	tests := map[string]struct {
		ownerReferences []metav1.OwnerReference
		expectedRefs    []*pb.KubernetesOwnerReference
		expectedError   bool
	}{
		"empty slice": {
			ownerReferences: []metav1.OwnerReference{},
			expectedRefs:    nil,
			expectedError:   false,
		},
		"single OwnerReference with all fields set": {
			ownerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "v1",
					BlockOwnerDeletion: ptrBool(true),
					Controller:         ptrBool(true),
					Kind:               "Pod",
					Name:               "pod-name",
					UID:                "uid-1234",
				},
			},
			expectedRefs: []*pb.KubernetesOwnerReference{
				{
					ApiVersion:         "v1",
					BlockOwnerDeletion: true,
					Controller:         true,
					Kind:               "Pod",
					Name:               "pod-name",
					Uid:                "uid-1234",
				},
			},
			expectedError: false,
		},
		"OwnerReference with nil BlockOwnerDeletion and Controller": {
			ownerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "v1",
					BlockOwnerDeletion: nil,
					Controller:         nil,
					Kind:               "Deployment",
					Name:               "deployment-name",
					UID:                "uid-5678",
				},
			},
			expectedRefs: []*pb.KubernetesOwnerReference{
				{
					ApiVersion:         "v1",
					BlockOwnerDeletion: false,
					Controller:         false,
					Kind:               "Deployment",
					Name:               "deployment-name",
					Uid:                "uid-5678",
				},
			},
			expectedError: false,
		},
		"multiple OwnerReferences": {
			ownerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "v1",
					BlockOwnerDeletion: ptrBool(false),
					Controller:         ptrBool(true),
					Kind:               "ReplicaSet",
					Name:               "replicaset-1",
					UID:                "uid-9999",
				},
				{
					APIVersion:         "apps/v1",
					BlockOwnerDeletion: ptrBool(true),
					Controller:         ptrBool(false),
					Kind:               "Deployment",
					Name:               "deployment-2",
					UID:                "uid-8888",
				},
			},
			expectedRefs: []*pb.KubernetesOwnerReference{
				{
					ApiVersion:         "v1",
					BlockOwnerDeletion: false,
					Controller:         true,
					Kind:               "ReplicaSet",
					Name:               "replicaset-1",
					Uid:                "uid-9999",
				},
				{
					ApiVersion:         "apps/v1",
					BlockOwnerDeletion: true,
					Controller:         false,
					Kind:               "Deployment",
					Name:               "deployment-2",
					Uid:                "uid-8888",
				},
			},
			expectedError: false,
		},
	}

	for name, tt := range tests {
		suite.T().Run(name, func(t *testing.T) {
			result, err := convertOwnerReferences(tt.ownerReferences)

			// Check for errors
			if tt.expectedError {
				assert.Error(t, err, "expected error for test: %s", name)
			} else {
				assert.NoError(t, err, "unexpected error for test: %s", name)
				// Compare the result
				assert.Equal(t, tt.expectedRefs, result, "test failed: %s", name)
			}
		})
	}
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

func (suite *ControllerTestSuite) TestGetProviderIdNodeSpec() {

	tests := map[string]struct {
		nodeName       string
		node           *v1.Node
		expectedID     string
		expectedErrMsg string
	}{
		"node not found": {
			nodeName:       "nonexistent-node",
			node:           nil,
			expectedID:     "",
			expectedErrMsg: "",
		},
		"node with providerID": {
			nodeName: "test-node",
			node: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Spec: v1.NodeSpec{
					ProviderID: "provider-id-123",
				},
			},
			expectedID:     "provider-id-123",
			expectedErrMsg: "",
		},
		"node without providerID": {
			nodeName: "test-node-no-id",
			node: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-no-id",
				},
				Spec: v1.NodeSpec{
					ProviderID: "",
				},
			},
			expectedID:     "",
			expectedErrMsg: "no providerID set",
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			clientset, _ := NewClientSet()
			if tt.node != nil {
				_, err := clientset.CoreV1().Nodes().Create(context.TODO(), tt.node, metav1.CreateOptions{})
				assert.NoError(suite.T(), err)
			}

			id, err := getProviderIdNodeSpec(context.TODO(), clientset, tt.nodeName)
			if tt.expectedErrMsg != "" {
				assert.EqualError(suite.T(), err, tt.expectedErrMsg)
			} else {
				assert.NoError(suite.T(), err)
				assert.Equal(suite.T(), tt.expectedID, id)
			}
		})
	}
}

func (suite *ControllerTestSuite) TestGetNodeIpAddresses() {
	tests := map[string]struct {
		nodeName       string
		node           *v1.Node
		expectedIPs    []string
		expectedErrMsg string
	}{
		"node not found": {
			nodeName:       "nonexistent-node",
			node:           nil,
			expectedIPs:    nil,
			expectedErrMsg: "failed to get node",
		},
		"node with internal and external IPs": {
			nodeName: "test-node",
			node: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: v1.NodeStatus{
					Addresses: []v1.NodeAddress{
						{Type: v1.NodeInternalIP, Address: "192.168.1.1"},
						{Type: v1.NodeExternalIP, Address: "1.2.3.4"},
					},
				},
			},
			expectedIPs:    []string{"192.168.1.1", "1.2.3.4"},
			expectedErrMsg: "",
		},
		"node with only internal IP": {
			nodeName: "test-node-internal",
			node: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-internal",
				},
				Status: v1.NodeStatus{
					Addresses: []v1.NodeAddress{
						{Type: v1.NodeInternalIP, Address: "192.168.1.1"},
					},
				},
			},
			expectedIPs:    []string{"192.168.1.1"},
			expectedErrMsg: "",
		},
		"node with only external IP": {
			nodeName: "test-node-external",
			node: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-external",
				},
				Status: v1.NodeStatus{
					Addresses: []v1.NodeAddress{
						{Type: v1.NodeExternalIP, Address: "1.2.3.4"},
					},
				},
			},
			expectedIPs:    []string{"1.2.3.4"},
			expectedErrMsg: "",
		},
		"node with no IPs": {
			nodeName: "test-node-no-ips",
			node: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-no-ips",
				},
				Status: v1.NodeStatus{
					Addresses: []v1.NodeAddress{},
				},
			},
			expectedIPs:    []string{},
			expectedErrMsg: "",
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			clientset, _ := NewClientSet()
			if tt.node != nil {
				_, err := clientset.CoreV1().Nodes().Create(context.TODO(), tt.node, metav1.CreateOptions{})
				assert.NoError(suite.T(), err)
			}

			ips, err := getNodeIpAddresses(context.TODO(), clientset, tt.nodeName)
			if tt.expectedErrMsg != "" {
				assert.EqualError(suite.T(), err, tt.expectedErrMsg)
			} else {
				assert.NoError(suite.T(), err)
				assert.Equal(suite.T(), tt.expectedIPs, ips)
			}
		})
	}
}

func (suite *ControllerTestSuite) TestGetPodIPAddresses() {
	tests := map[string]struct {
		podName        string
		namespace      string
		pod            *v1.Pod
		expectedIPs    int
		expectedErrMsg string
	}{
		// TODO: Create happy test case for pod IP that is not spotty.
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

			ips, err := getPodIPAddresses(context.TODO(), tt.podName, clientset, tt.namespace)
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

func (suite *ControllerTestSuite) TestConvertIngressToStringList() {
	tests := map[string]struct {
		ingress        []v1.LoadBalancerIngress
		expectedResult []string
	}{
		"both IP and Hostname": {
			ingress: []v1.LoadBalancerIngress{
				{IP: "192.168.1.1", Hostname: "example.com"},
			},
			expectedResult: []string{"192.168.1.1", "example.com"},
		},
		"only IP": {
			ingress: []v1.LoadBalancerIngress{
				{IP: "192.168.1.2"},
			},
			expectedResult: []string{"192.168.1.2"},
		},
		"only Hostname": {
			ingress: []v1.LoadBalancerIngress{
				{Hostname: "another-example.com"},
			},
			expectedResult: []string{"another-example.com"},
		},
		"empty ingress": {
			ingress:        []v1.LoadBalancerIngress{},
			expectedResult: []string{},
		},
		"nil IP and Hostname": {
			ingress: []v1.LoadBalancerIngress{
				{IP: "", Hostname: ""},
			},
			expectedResult: []string{},
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			result := convertIngressToStringList(tt.ingress)
			assert.Equal(suite.T(), tt.expectedResult, result)
		})
	}
}

func (suite *ControllerTestSuite) TestConvertServicePortsToPorts() {
	var nodePort int32 = int32(30000)
	var nodePort2 int32 = int32(30001)
	tests := map[string]struct {
		servicePorts   []v1.ServicePort
		expectedResult []*pb.KubernetesServiceData_ServicePort
	}{
		"single service port with node port": {
			servicePorts: []v1.ServicePort{
				{NodePort: 30000, Port: 80, Protocol: v1.ProtocolTCP},
			},
			expectedResult: []*pb.KubernetesServiceData_ServicePort{
				{NodePort: ptrInt32ToUint32(&nodePort), Port: 80, Protocol: "TCP"},
			},
		},
		"multiple service ports with node ports": {
			servicePorts: []v1.ServicePort{
				{NodePort: 30000, Port: 80, Protocol: v1.ProtocolTCP},
				{NodePort: 30001, Port: 443, Protocol: v1.ProtocolTCP},
			},
			expectedResult: []*pb.KubernetesServiceData_ServicePort{
				{NodePort: ptrInt32ToUint32(&nodePort), Port: 80, Protocol: "TCP"},
				{NodePort: ptrInt32ToUint32(&nodePort2), Port: 443, Protocol: "TCP"},
			},
		},
		"service port without node port": {
			servicePorts: []v1.ServicePort{
				{NodePort: 0, Port: 80, Protocol: v1.ProtocolTCP},
			},
			expectedResult: []*pb.KubernetesServiceData_ServicePort{
				{Port: 80, Protocol: "TCP"},
			},
		},
		"single service port without protocol": {
			servicePorts: []v1.ServicePort{
				{NodePort: 30000, Port: 80},
			},
			expectedResult: []*pb.KubernetesServiceData_ServicePort{
				{NodePort: ptrInt32ToUint32(&nodePort), Port: 80, Protocol: "TCP"},
			},
		},
		"mix of service ports with and without node ports": {
			servicePorts: []v1.ServicePort{
				{NodePort: 30000, Port: 80, Protocol: v1.ProtocolTCP},
				{NodePort: 0, Port: 443, Protocol: v1.ProtocolTCP},
			},
			expectedResult: []*pb.KubernetesServiceData_ServicePort{
				{NodePort: ptrInt32ToUint32(&nodePort), Port: 80, Protocol: "TCP"},
				{Port: 443, Protocol: "TCP"},
			},
		},
		"empty service ports": {
			servicePorts:   []v1.ServicePort{},
			expectedResult: []*pb.KubernetesServiceData_ServicePort{},
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			result := convertServicePortsToPorts(tt.servicePorts)
			assert.Equal(suite.T(), tt.expectedResult, result)
		})
	}
}

func (suite *ControllerTestSuite) TestCombineIPAddresses() {
	tests := map[string]struct {
		clusterIps            []string
		externalIps           []string
		loadBalancerIngresses []string
		loadBalancerIp        string
		expectedResult        []string
	}{
		"all fields populated": {
			clusterIps:            []string{"10.0.0.1", "10.0.0.2"},
			externalIps:           []string{"192.168.1.1"},
			loadBalancerIngresses: []string{"lb1.example.com", "lb2.example.com"},
			loadBalancerIp:        "34.123.45.67",
			expectedResult:        []string{"10.0.0.1", "10.0.0.2", "192.168.1.1", "lb1.example.com", "lb2.example.com", "34.123.45.67"},
		},
		"no load balancer IP": {
			clusterIps:            []string{"10.0.0.1"},
			externalIps:           []string{"192.168.1.1"},
			loadBalancerIngresses: []string{"lb1.example.com"},
			loadBalancerIp:        "",
			expectedResult:        []string{"10.0.0.1", "192.168.1.1", "lb1.example.com"},
		},
		"only cluster IPs": {
			clusterIps:            []string{"10.0.0.1", "10.0.0.2"},
			externalIps:           []string{},
			loadBalancerIngresses: []string{},
			loadBalancerIp:        "",
			expectedResult:        []string{"10.0.0.1", "10.0.0.2"},
		},
		"no IPs": {
			clusterIps:            []string{},
			externalIps:           []string{},
			loadBalancerIngresses: []string{},
			loadBalancerIp:        "",
			expectedResult:        []string{},
		},
		"mixed empty lists": {
			clusterIps:            []string{"10.0.0.1"},
			externalIps:           []string{},
			loadBalancerIngresses: []string{},
			loadBalancerIp:        "34.123.45.67",
			expectedResult:        []string{"10.0.0.1", "34.123.45.67"},
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			result := combineIPAddresses(tt.clusterIps, tt.externalIps, tt.loadBalancerIngresses, tt.loadBalancerIp)
			assert.Equal(suite.T(), tt.expectedResult, result)
		})
	}
}

func (suite *ControllerTestSuite) TestConvertToKubernetesServiceData() {
	tests := map[string]struct {
		service        *v1.Service
		expectedResult *pb.KubernetesServiceData
		expectedError  error
	}{
		"service not found": {
			expectedResult: nil,
			expectedError:  errors.New("failed to get service"),
		},
		"normal case, all fields populated": {
			service: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-service",
					Namespace: "default",
				},
				Spec: v1.ServiceSpec{
					ClusterIPs:     []string{},
					ExternalIPs:    []string{"192.168.1.1"},
					LoadBalancerIP: "34.123.45.67",
					Ports: []v1.ServicePort{
						{
							Name:     "port1",
							NodePort: 30001,
							Port:     8080,
							Protocol: v1.ProtocolTCP,
						},
						{
							Name:     "port2",
							NodePort: 30002,
							Port:     443,
							Protocol: v1.ProtocolTCP,
						},
					},
					Type: v1.ServiceTypeLoadBalancer,
				},
				Status: v1.ServiceStatus{
					LoadBalancer: v1.LoadBalancerStatus{
						Ingress: []v1.LoadBalancerIngress{
							{Hostname: "lb1.example.com"},
							{Hostname: "lb2.example.com"},
						},
					},
				},
			},
			expectedResult: &pb.KubernetesServiceData{
				// IpAddresses: []string{}, // Ignored in this test case
				Ports: []*pb.KubernetesServiceData_ServicePort{
					{
						NodePort: ptrUint32(30001),
						Port:     8080,
						Protocol: "TCP",
					},
					{
						NodePort: ptrUint32(30002),
						Port:     443,
						Protocol: "TCP",
					},
				},
				Type:              "LoadBalancer",
				ExternalName:      ptrString(""),
				LoadBalancerClass: nil,
			},
			expectedError: nil,
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			ctx := context.TODO()
			if tt.service != nil {
				_, err := suite.clientset.CoreV1().Services(tt.service.Namespace).Create(ctx, tt.service, metav1.CreateOptions{})
				assert.NoError(suite.T(), err)
			}

			result, err := convertToKubernetesServiceData(ctx, "test-service", suite.clientset, "default")
			if tt.expectedError != nil {
				assert.EqualError(suite.T(), err, tt.expectedError.Error())
			} else {
				assert.NoError(suite.T(), err)
				// Custom comparison ignoring IpAddresses field since KIND can mess with them.
				assertEqualKubernetesServiceData(suite.T(), tt.expectedResult, result)
			}
		})
	}
}

func assertEqualKubernetesServiceData(t *testing.T, expected, actual *pb.KubernetesServiceData) {
	assert.Equal(t, expected.Ports, actual.Ports)
	assert.Equal(t, expected.Type, actual.Type)
	assert.Equal(t, expected.ExternalName, actual.ExternalName)
	assert.Equal(t, expected.LoadBalancerClass, actual.LoadBalancerClass)
}
