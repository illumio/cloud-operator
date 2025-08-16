// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"errors"
	"flag"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// ptrBool is a helper function to create pointers to bool values (used in metav1.OwnerReference)
// Used to simulate the nil behavior of the optional fields.
func ptrBool(b bool) *bool {
	return &b
}

// ptrString is a helper function to create pointers to string values
// Used to simulate the nil behavior of the optional fields.
func ptrString(s string) *string {
	return &s
}

// ptrUint32 is a helper function to create pointers to int32 values
// Used to simulate the nil behavior of the optional fields.
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
	got := convertMetaObjectToMetadata(context.Background(), configMap, clientset, "configMap")

	// Define what you expect to get.
	want := metav1.ObjectMeta{
		Name:            "test-pod",
		Namespace:       "test-namespace",
		UID:             "test-uid",
		ResourceVersion: "test-version",
	}

	// Compare the result with the expected outcome.
	if got.GetName() != want.Name || got.GetNamespace() != want.Namespace || got.GetUid() != string(want.UID) || got.GetResourceVersion() != want.ResourceVersion {
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
	result := convertMetaObjectToMetadata(context.Background(), objMeta, clientset, resource)

	suite.Equal(expected, result)
}

func (suite *ControllerTestSuite) TestConvertOwnerReferences() {
	tests := map[string]struct {
		ownerReferences []metav1.OwnerReference
		expectedRefs    []*pb.KubernetesOwnerReference
	}{
		"empty slice": {
			ownerReferences: []metav1.OwnerReference{},
			expectedRefs:    nil,
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
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			result := convertOwnerReferences(tt.ownerReferences)

			suite.Equal(tt.expectedRefs, result, "test failed: %s", name)
		})
	}
}

func TestConvertToProtoTimestamp(t *testing.T) {
	k8sTime := metav1.Time{Time: time.Now()}
	expected := timestamppb.New(k8sTime.Time)

	result := convertToProtoTimestamp(k8sTime)
	assert.Equal(t, expected, result)
}

func TestConvertPodIPsToStrings(t *testing.T) {
	tests := map[string]struct {
		podIPs      []v1.PodIP
		expectedIPs []string
	}{
		"empty slice": {
			podIPs:      []v1.PodIP{},
			expectedIPs: []string{},
		},
		"single IP": {
			podIPs: []v1.PodIP{
				{IP: "192.168.1.1"},
			},
			expectedIPs: []string{"192.168.1.1"},
		},
		"multiple IPs": {
			podIPs: []v1.PodIP{
				{IP: "192.168.1.1"},
				{IP: "192.168.1.2"},
				{IP: "192.168.1.3"},
			},
			expectedIPs: []string{"192.168.1.1", "192.168.1.2", "192.168.1.3"},
		},
		"IPs with different formats": {
			podIPs: []v1.PodIP{
				{IP: "192.168.1.1"},
				{IP: "fe80::1ff:fe23:4567:890a"},
				{IP: "10.0.0.1"},
			},
			expectedIPs: []string{"192.168.1.1", "fe80::1ff:fe23:4567:890a", "10.0.0.1"},
		},
	}

	for name, tt := range tests {
		result := convertPodIPsToStrings(tt.podIPs)
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
			expectedErrMsg: "nodes \"nonexistent-node\" not found",
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
				suite.Require().NoError(err)
			}

			id, err := getProviderIdNodeSpec(context.TODO(), clientset, tt.nodeName)
			if tt.expectedErrMsg != "" {
				suite.EqualError(err, tt.expectedErrMsg)
			} else {
				suite.Require().NoError(err)
				suite.Equal(tt.expectedID, id)
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
			nodeName: "test-node-with-internal-external",
			node: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-with-internal-external",
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
				suite.Require().NoError(err)
			}

			ips, err := getNodeIpAddresses(context.TODO(), clientset, tt.nodeName)
			if tt.expectedErrMsg != "" {
				suite.EqualError(err, tt.expectedErrMsg)
			} else {
				suite.Require().NoError(err)
				suite.Equal(tt.expectedIPs, ips)
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
			podName:     "nonexistent-pod",
			namespace:   "default",
			pod:         nil,
			expectedIPs: 0,
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
				suite.Require().NoError(err)
			}

			ips := getPodIPAddresses(context.TODO(), tt.podName, clientset, tt.namespace)
			suite.Len(ips, tt.expectedIPs)
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
				suite.Error(err)
			} else {
				suite.Require().NoError(err)
				suite.NotNil(resources)

				if name == "invalid namespace" {
					suite.Empty(resources.Items)
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
							"metadata_misspelled": map[string]interface{}{
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
				suite.Require().Error(err)
				suite.EqualError(err, tc.expectedErrMsg)
			} else {
				suite.Require().NoError(err)
				suite.Equal(tc.expectedMetas, objectMetas)
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
			suite.Equal(tt.expectedResult, result)
		})
	}
}

func (suite *ControllerTestSuite) TestConvertServicePortsToPorts() {
	var nodePort = int32(30000)

	var nodePort2 = int32(30001)

	tests := map[string]struct {
		servicePorts   []v1.ServicePort
		expectedResult []*pb.KubernetesServiceData_ServicePort
	}{
		"single service port with node port": {
			servicePorts: []v1.ServicePort{
				{NodePort: 30000, Port: 80, Protocol: v1.ProtocolTCP},
			},
			expectedResult: []*pb.KubernetesServiceData_ServicePort{
				{NodePort: int32ToUint32(&nodePort), Port: 80, Protocol: "TCP"},
			},
		},
		"multiple service ports with node ports": {
			servicePorts: []v1.ServicePort{
				{NodePort: 30000, Port: 80, Protocol: v1.ProtocolTCP},
				{NodePort: 30001, Port: 443, Protocol: v1.ProtocolTCP},
			},
			expectedResult: []*pb.KubernetesServiceData_ServicePort{
				{NodePort: int32ToUint32(&nodePort), Port: 80, Protocol: "TCP"},
				{NodePort: int32ToUint32(&nodePort2), Port: 443, Protocol: "TCP"},
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
				{NodePort: int32ToUint32(&nodePort), Port: 80, Protocol: "TCP"},
			},
		},
		"mix of service ports with and without node ports": {
			servicePorts: []v1.ServicePort{
				{NodePort: 30000, Port: 80, Protocol: v1.ProtocolTCP},
				{NodePort: 0, Port: 443, Protocol: v1.ProtocolTCP},
			},
			expectedResult: []*pb.KubernetesServiceData_ServicePort{
				{NodePort: int32ToUint32(&nodePort), Port: 80, Protocol: "TCP"},
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
			suite.Equal(tt.expectedResult, result)
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
			suite.Equal(tt.expectedResult, result)
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

			// Ensure the service is deleted before running the test
			if tt.service == nil {
				err := suite.clientset.CoreV1().Services("default").Delete(ctx, "test-service", metav1.DeleteOptions{})
				if err != nil && !k8sErrors.IsNotFound(err) {
					suite.T().Fatal("Failed to delete service: " + err.Error())
				}

				time.Sleep(100 * time.Millisecond) // Wait for deletion to propagate
			}

			if tt.service != nil {
				_, err := suite.clientset.CoreV1().Services(tt.service.Namespace).Create(ctx, tt.service, metav1.CreateOptions{})
				suite.Require().NoError(err)
			}

			result, err := convertToKubernetesServiceData(ctx, "test-service", suite.clientset, "default")
			if tt.expectedError != nil {
				suite.EqualError(err, tt.expectedError.Error())
			} else {
				suite.Require().NoError(err)
				// Custom comparison ignoring IpAddresses field since KIND can mess with them.
				assertEqualKubernetesServiceData(suite.T(), tt.expectedResult, result)
			}
		})
	}
}

func assertEqualKubernetesServiceData(t *testing.T, expected, actual *pb.KubernetesServiceData) {
	t.Helper()

	assert.Equal(t, expected.GetPorts(), actual.GetPorts())
	assert.Equal(t, expected.GetType(), actual.GetType())
	assert.Equal(t, expected.GetExternalName(), actual.GetExternalName())
	assert.Equal(t, expected.GetLoadBalancerClass(), actual.GetLoadBalancerClass())
}

func TestConvertNetworkPolicyEgressRuleToProto(t *testing.T) {
	egressRules := []networkingv1.NetworkPolicyEgressRule{
		{
			To: []networkingv1.NetworkPolicyPeer{
				{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"team": "backend"},
					},
				},
				{
					IPBlock: &networkingv1.IPBlock{
						CIDR:   "10.0.0.0/16",
						Except: []string{"10.0.1.0/24"},
					},
				},
			},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port: &intstr.IntOrString{IntVal: 443},
					Protocol: func() *v1.Protocol {
						proto := v1.ProtocolTCP

						return &proto
					}(),
				},
			},
		},
	}
	port := "443"

	result := convertNetworkPolicyEgressRuleToProto(egressRules)
	assert.Len(t, result, 1)
	assert.Equal(t, "backend", result[0].GetPeers()[0].GetPods().GetNamespaceSelector().GetMatchLabels()["team"])
	assert.Equal(t, "10.0.0.0/16", result[0].GetPeers()[1].GetIpBlock().GetCidr())
	assert.Equal(t, "10.0.1.0/24", result[0].GetPeers()[1].GetIpBlock().GetExcept()[0])
	assert.Equal(t, port, result[0].GetPorts()[0].GetPort())
	assert.Equal(t, pb.Port_PROTOCOL_TCP_UNSPECIFIED, result[0].GetPorts()[0].GetProtocol())
}

func TestConvertNetworkPolicyIngressRuleToProto(t *testing.T) {
	ingressRules := []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{
				{
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "frontend"},
					},
				},
				{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"team": "frontend"},
					},
				},
			},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port: &intstr.IntOrString{IntVal: 80},
					Protocol: func() *v1.Protocol {
						proto := v1.ProtocolTCP

						return &proto
					}(),
				},
				{
					Port: &intstr.IntOrString{IntVal: 8080},
					EndPort: func() *int32 {
						endPort := int32(8090)

						return &endPort
					}(),
					Protocol: func() *v1.Protocol {
						proto := v1.ProtocolTCP

						return &proto
					}(),
				},
			},
		},
	}
	port1 := "80"
	port2 := "8080"

	result := convertNetworkPolicyIngressRuleToProto(ingressRules)
	assert.Len(t, result, 1)
	assert.Equal(t, "frontend", result[0].GetPeers()[0].GetPods().GetPodSelector().GetMatchLabels()["app"])
	assert.Equal(t, "frontend", result[0].GetPeers()[1].GetPods().GetNamespaceSelector().GetMatchLabels()["team"])
	assert.Equal(t, port1, result[0].GetPorts()[0].GetPort())
	assert.Equal(t, pb.Port_PROTOCOL_TCP_UNSPECIFIED, result[0].GetPorts()[0].GetProtocol())
	assert.Equal(t, port2, result[0].GetPorts()[1].GetPort())
	assert.Equal(t, int32(8090), result[0].GetPorts()[1].GetEndPort())
	assert.Equal(t, pb.Port_PROTOCOL_TCP_UNSPECIFIED, result[0].GetPorts()[1].GetProtocol())
}

func TestConvertNetworkPolicyPeerToProto(t *testing.T) {
	peers := []networkingv1.NetworkPolicyPeer{
		{
			IPBlock: &networkingv1.IPBlock{
				CIDR:   "192.168.0.0/16",
				Except: []string{"192.168.1.0/24"},
			},
		},
		{
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"team": "backend"},
			},
		},
	}

	result := convertNetworkPolicyPeerToProto(peers)
	assert.Len(t, result, 2)
	assert.Equal(t, "192.168.0.0/16", result[0].GetIpBlock().GetCidr())
	assert.Equal(t, "192.168.1.0/24", result[0].GetIpBlock().GetExcept()[0])
	assert.Equal(t, "backend", result[1].GetPods().GetNamespaceSelector().GetMatchLabels()["team"])
}

func TestConvertNetworkPolicyNamespaceSelectorToProto(t *testing.T) {
	labelSelector := &metav1.LabelSelector{
		MatchLabels: map[string]string{"team": "dev"},
	}

	result := convertLabelSelectorToProto(labelSelector)
	assert.NotNil(t, result)
	assert.Equal(t, "dev", result.GetMatchLabels()["team"])
}

func TestConvertIPBlocksToProto(t *testing.T) {
	ipBlock := &networkingv1.IPBlock{
		CIDR:   "192.168.1.0/24",
		Except: []string{"192.168.1.1"},
	}

	result := convertIPBlockToProto(ipBlock)
	assert.NotNil(t, result)
	assert.Equal(t, "192.168.1.0/24", result.GetCidr())
	assert.Equal(t, "192.168.1.1", result.GetExcept()[0])
}

func TestConvertLabelSelectorToProto(t *testing.T) {
	podSelector := &metav1.LabelSelector{
		MatchLabels: map[string]string{"app": "test"},
	}

	result := convertLabelSelectorToProto(podSelector)
	assert.NotNil(t, result)
	assert.Equal(t, "test", result.GetMatchLabels()["app"])
}

func TestConvertNetworkPolicyToProto(t *testing.T) {
	tests := map[string]struct {
		input          *networkingv1.NetworkPolicy
		expectedPolicy *pb.KubernetesNetworkPolicyData
		expectError    bool
	}{
		"simple policy with pod selector": {
			input: &networkingv1.NetworkPolicy{
				Spec: networkingv1.NetworkPolicySpec{
					PodSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "frontend"},
					},
					PolicyTypes: []networkingv1.PolicyType{
						networkingv1.PolicyTypeIngress,
					},
					Ingress: []networkingv1.NetworkPolicyIngressRule{
						{
							From: []networkingv1.NetworkPolicyPeer{
								{
									NamespaceSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{"team": "backend"},
									},
								},
							},
							Ports: []networkingv1.NetworkPolicyPort{},
						},
					},
				},
			},
			expectedPolicy: &pb.KubernetesNetworkPolicyData{
				Ingress: true,
				PodSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{"app": "frontend"},
				},
				IngressRules: []*pb.NetworkPolicyRule{
					{
						Peers: []*pb.Peer{
							{
								Peer: &pb.Peer_Pods{
									Pods: &pb.PeerSelector{
										NamespaceSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"team": "backend"},
										},
									},
								},
							},
						},
						Ports: nil,
					},
				},
				EgressRules: nil,
			},
			expectError: false,
		},
		"complex policy with ingress and egress": {
			input: &networkingv1.NetworkPolicy{
				Spec: networkingv1.NetworkPolicySpec{
					PodSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{"role": "db"},
					},
					PolicyTypes: []networkingv1.PolicyType{
						networkingv1.PolicyTypeIngress,
						networkingv1.PolicyTypeEgress,
					},
					Ingress: []networkingv1.NetworkPolicyIngressRule{
						{
							From: []networkingv1.NetworkPolicyPeer{
								{
									PodSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{"app": "frontend"},
									},
								},
							},
							Ports: []networkingv1.NetworkPolicyPort{},
						},
					},
					Egress: []networkingv1.NetworkPolicyEgressRule{
						{
							To: []networkingv1.NetworkPolicyPeer{
								{
									IPBlock: &networkingv1.IPBlock{
										CIDR:   "10.0.0.0/16",
										Except: []string{"10.0.1.0/24"},
									},
								},
							},
							Ports: []networkingv1.NetworkPolicyPort{},
						},
					},
				},
			},
			expectedPolicy: &pb.KubernetesNetworkPolicyData{
				Ingress: true,
				Egress:  true,
				PodSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{"role": "db"},
				},
				IngressRules: []*pb.NetworkPolicyRule{
					{
						Peers: []*pb.Peer{
							{
								Peer: &pb.Peer_Pods{
									Pods: &pb.PeerSelector{
										PodSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"app": "frontend"},
										},
									},
								},
							},
						},
						Ports: nil,
					},
				},
				EgressRules: []*pb.NetworkPolicyRule{
					{
						Peers: []*pb.Peer{
							{
								Peer: &pb.Peer_IpBlock{
									IpBlock: &pb.IPBlock{
										Cidr:   "10.0.0.0/16",
										Except: []string{"10.0.1.0/24"},
									},
								},
							},
						},
						Ports: nil,
					},
				},
			},
			expectError: false,
		},
		"nil policy": {
			input:          nil,
			expectedPolicy: nil,
			expectError:    true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := convertNetworkPolicyToProto(tt.input)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedPolicy, result)
			}
		})
	}
}

func TestConvertNetworkPolicyToProto_Comprehensive(t *testing.T) {
	tests := map[string]struct {
		input          *networkingv1.NetworkPolicy
		expectedPolicy *pb.KubernetesNetworkPolicyData
		expectError    bool
	}{
		"empty policy": {
			input: &networkingv1.NetworkPolicy{
				Spec: networkingv1.NetworkPolicySpec{},
			},
			expectedPolicy: &pb.KubernetesNetworkPolicyData{
				PodSelector:  nil,
				IngressRules: nil,
				EgressRules:  nil,
			},
			expectError: false,
		},
		"policy with pod selector and ingress rule": {
			input: &networkingv1.NetworkPolicy{
				Spec: networkingv1.NetworkPolicySpec{
					PodSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "frontend"},
					},
					Ingress: []networkingv1.NetworkPolicyIngressRule{
						{
							From: []networkingv1.NetworkPolicyPeer{
								{
									NamespaceSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{"team": "backend"},
									},
								},
							},
						},
					},
					PolicyTypes: []networkingv1.PolicyType{
						networkingv1.PolicyTypeIngress,
					},
				},
			},
			expectedPolicy: &pb.KubernetesNetworkPolicyData{
				Ingress: true,
				PodSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{"app": "frontend"},
				},
				IngressRules: []*pb.NetworkPolicyRule{
					{
						Peers: []*pb.Peer{
							{
								Peer: &pb.Peer_Pods{
									Pods: &pb.PeerSelector{
										NamespaceSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"team": "backend"},
										},
									},
								},
							},
						},
					},
				},
				EgressRules: nil,
			},
			expectError: false,
		},
		"policy with egress rule and IPBlock": {
			input: &networkingv1.NetworkPolicy{
				Spec: networkingv1.NetworkPolicySpec{
					Egress: []networkingv1.NetworkPolicyEgressRule{
						{
							To: []networkingv1.NetworkPolicyPeer{
								{
									IPBlock: &networkingv1.IPBlock{
										CIDR:   "10.0.0.0/16",
										Except: []string{"10.0.1.0/24"},
									},
								},
							},
						},
					},
					PolicyTypes: []networkingv1.PolicyType{
						networkingv1.PolicyTypeEgress,
					},
				},
			},
			expectedPolicy: &pb.KubernetesNetworkPolicyData{
				PodSelector: nil,
				Ingress:     false,
				Egress:      true,
				EgressRules: []*pb.NetworkPolicyRule{
					{
						Peers: []*pb.Peer{
							{
								Peer: &pb.Peer_IpBlock{
									IpBlock: &pb.IPBlock{
										Cidr:   "10.0.0.0/16",
										Except: []string{"10.0.1.0/24"},
									},
								},
							},
						},
					},
				},
			},
			expectError: false,
		},
		"policy with mixed ingress and egress rules": {
			input: &networkingv1.NetworkPolicy{
				Spec: networkingv1.NetworkPolicySpec{
					PodSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{"role": "db"},
					},
					Ingress: []networkingv1.NetworkPolicyIngressRule{
						{
							From: []networkingv1.NetworkPolicyPeer{
								{
									PodSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{"app": "frontend"},
									},
								},
							},
						},
					},
					Egress: []networkingv1.NetworkPolicyEgressRule{
						{
							To: []networkingv1.NetworkPolicyPeer{
								{
									IPBlock: &networkingv1.IPBlock{
										CIDR:   "192.168.0.0/16",
										Except: []string{"192.168.1.0/24"},
									},
								},
							},
						},
					},
					PolicyTypes: []networkingv1.PolicyType{
						networkingv1.PolicyTypeIngress,
						networkingv1.PolicyTypeEgress,
					},
				},
			},
			expectedPolicy: &pb.KubernetesNetworkPolicyData{
				Ingress: true,
				Egress:  true,
				PodSelector: &pb.LabelSelector{
					MatchLabels: map[string]string{"role": "db"},
				},
				IngressRules: []*pb.NetworkPolicyRule{
					{
						Peers: []*pb.Peer{
							{
								Peer: &pb.Peer_Pods{
									Pods: &pb.PeerSelector{
										PodSelector: &pb.LabelSelector{
											MatchLabels: map[string]string{"app": "frontend"},
										},
									},
								},
							},
						},
					},
				},
				EgressRules: []*pb.NetworkPolicyRule{
					{
						Peers: []*pb.Peer{
							{
								Peer: &pb.Peer_IpBlock{
									IpBlock: &pb.IPBlock{
										Cidr:   "192.168.0.0/16",
										Except: []string{"192.168.1.0/24"},
									},
								},
							},
						},
					},
				},
			},
			expectError: false,
		},
		"policy with match expressions": {
			input: &networkingv1.NetworkPolicy{
				Spec: networkingv1.NetworkPolicySpec{
					PodSelector: metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "environment",
								Operator: metav1.LabelSelectorOpIn,
								Values:   []string{"production", "staging"},
							},
						},
					},
				},
			},
			expectedPolicy: &pb.KubernetesNetworkPolicyData{
				PodSelector: &pb.LabelSelector{
					MatchExpressions: []*pb.LabelSelectorRequirement{
						{
							Key:      "environment",
							Operator: "In",
							Values:   []string{"production", "staging"},
						},
					},
				},
				IngressRules: nil,
				EgressRules:  nil,
			},
			expectError: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := convertNetworkPolicyToProto(tt.input)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedPolicy, result)
			}
		})
	}
}
