// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"errors"
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
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/k8sclient"
)

func (suite *ControllerTestSuite) TestConvertObjectToMetadata() {
	// Setup a mock object, e.g., a ConfigMap with predefined metadata
	configMap := metav1.ObjectMeta{
		Name:            "test-pod",
		Namespace:       "test-namespace",
		UID:             "test-uid",
		ResourceVersion: "test-version",
	}
	logger := zap.NewNop()

	k8sClient, err := k8sclient.NewClient()
	if err != nil {
		logger.Error("Failed to create clientset", zap.Error(err))
		suite.T().Error("could not create clientset")
	}
	// Execute the function under test.
	got := ConvertMetaObjectToMetadata(context.Background(), configMap, k8sClient.GetClientset(), "configMap", "", "v1")

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

func TestNewCoreResourceConverter_HappyPath(t *testing.T) {
	clientset := k8sfake.NewSimpleClientset()
	logger := zap.NewNop()

	converter := NewCoreResourceConverter(clientset, logger)

	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]any{
				"name":            "my-deploy",
				"namespace":       "prod",
				"uid":             "deploy-uid-123",
				"resourceVersion": "999",
				"labels": map[string]any{
					"app": "web",
				},
			},
		},
	}

	result, err := converter(context.Background(), obj)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "my-deploy", result.GetName())
	assert.Equal(t, "prod", result.GetNamespace())
	assert.Equal(t, "deploy-uid-123", result.GetUid())
	assert.Equal(t, "999", result.GetResourceVersion())
	assert.Equal(t, "Deployment", result.GetKind())
	assert.Equal(t, "apps", result.GetApiGroup())
	assert.Equal(t, "v1", result.GetApiVersion())
	assert.Equal(t, "web", result.GetLabels()["app"])
}

func TestNewCoreResourceConverter_MissingMetadata(t *testing.T) {
	clientset := k8sfake.NewSimpleClientset()
	logger := zap.NewNop()

	converter := NewCoreResourceConverter(clientset, logger)

	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
		},
	}

	_, err := converter(context.Background(), obj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot get metadata from resource")
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

	metaData, err := GetObjectMetadataFromRuntimeObject(pod)
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
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]any{
				"name":      "test-pod",
				"namespace": "test-namespace",
				"labels": map[string]string{
					"app": "test-app",
				},
			},
		},
	}

	// Call the function under test.
	metadata, _ := GetMetadataFromResource(logger, resource)

	// Validate the results.
	if metadata.Name != "test-pod" || metadata.Namespace != "test-namespace" || metadata.Labels["app"] != "test-app" {
		t.Errorf("Incorrect metadata extracted: %+v", metadata)
	}
}

func (suite *ControllerTestSuite) TestConvertMetaObjectToMetadata() {
	logger := zap.NewNop()

	k8sClient, err := k8sclient.NewClient()
	if err != nil {
		logger.Error("Failed to create clientset", zap.Error(err))
		suite.T().Fatalf("could not create clientset: %v", err)
	}

	clientset := k8sClient.GetClientset()
	sampleData := make(map[string]string)
	creationTimestamp := metav1.Time{Time: time.Now()}

	tests := map[string]struct {
		objMeta    metav1.ObjectMeta
		kind       string
		apiGroup   string
		apiVersion string
		expected   *pb.KubernetesObjectData
	}{
		"with namespace": {
			objMeta: metav1.ObjectMeta{
				Annotations:       sampleData,
				CreationTimestamp: creationTimestamp,
				Labels:            sampleData,
				Name:              "test-name",
				Namespace:         "test-namespace",
				ResourceVersion:   "test-version",
				UID:               "test-uid",
			},
			kind:       "test-resource",
			apiGroup:   "",
			apiVersion: "v1",
			expected: &pb.KubernetesObjectData{
				Annotations:       sampleData,
				CreationTimestamp: convertToProtoTimestamp(creationTimestamp),
				Kind:              "test-resource",
				Labels:            sampleData,
				Name:              "test-name",
				Namespace:         new("test-namespace"),
				ResourceVersion:   "test-version",
				Uid:               "test-uid",
				ApiGroup:          "",
				ApiVersion:        "v1",
			},
		},
		"without namespace (cluster-scoped)": {
			objMeta: metav1.ObjectMeta{
				Annotations:       sampleData,
				CreationTimestamp: creationTimestamp,
				Labels:            sampleData,
				Name:              "test-node",
				Namespace:         "",
				ResourceVersion:   "test-version",
				UID:               "test-uid",
			},
			kind:       "Node",
			apiGroup:   "",
			apiVersion: "v1",
			expected: &pb.KubernetesObjectData{
				Annotations:       sampleData,
				CreationTimestamp: convertToProtoTimestamp(creationTimestamp),
				Kind:              "Node",
				Labels:            sampleData,
				Name:              "test-node",
				ResourceVersion:   "test-version",
				Uid:               "test-uid",
				ApiGroup:          "",
				ApiVersion:        "v1",
			},
		},
		"group resource (apps/v1)": {
			objMeta: metav1.ObjectMeta{
				Annotations:       sampleData,
				CreationTimestamp: creationTimestamp,
				Labels:            sampleData,
				Name:              "test-deploy",
				Namespace:         "test-namespace",
				ResourceVersion:   "test-version",
				UID:               "test-uid",
			},
			kind:       "Deployment",
			apiGroup:   "apps",
			apiVersion: "v1",
			expected: &pb.KubernetesObjectData{
				Annotations:       sampleData,
				CreationTimestamp: convertToProtoTimestamp(creationTimestamp),
				Kind:              "Deployment",
				Labels:            sampleData,
				Name:              "test-deploy",
				Namespace:         new("test-namespace"),
				ResourceVersion:   "test-version",
				Uid:               "test-uid",
				ApiGroup:          "apps",
				ApiVersion:        "v1",
			},
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			result := ConvertMetaObjectToMetadata(context.Background(), tt.objMeta, clientset, tt.kind, tt.apiGroup, tt.apiVersion)
			suite.Equal(tt.expected, result)
		})
	}
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
					BlockOwnerDeletion: new(true),
					Controller:         new(true),
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
					BlockOwnerDeletion: new(false),
					Controller:         new(true),
					Kind:               "ReplicaSet",
					Name:               "replicaset-1",
					UID:                "uid-9999",
				},
				{
					APIVersion:         "apps/v1",
					BlockOwnerDeletion: new(true),
					Controller:         new(false),
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
			k8sClient, _ := k8sclient.NewClient()

			clientset := k8sClient.GetClientset()
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
			k8sClient, _ := k8sclient.NewClient()

			clientset := k8sClient.GetClientset()
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

	k8sClient, err := k8sclient.NewClient()
	if err != nil {
		suite.T().Fatal("Failed to get client set " + err.Error())
	}

	clientset := k8sClient.GetClientset()

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

func (suite *ControllerTestSuite) TestConvertIngressToStringList() {
	tests := map[string]struct {
		ingress        []v1.LoadBalancerIngress
		expectedResult []string
	}{
		"both IP and Hostname": {
			ingress: []v1.LoadBalancerIngress{
				{IP: "192.168.1.1", Hostname: "something.invalid"},
			},
			expectedResult: []string{"192.168.1.1", "something.invalid"},
		},
		"only IP": {
			ingress: []v1.LoadBalancerIngress{
				{IP: "192.168.1.2"},
			},
			expectedResult: []string{"192.168.1.2"},
		},
		"only Hostname": {
			ingress: []v1.LoadBalancerIngress{
				{Hostname: "another.invalid"},
			},
			expectedResult: []string{"another.invalid"},
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
			loadBalancerIngresses: []string{"lb1.something.invalid", "lb2.something.invalid"},
			loadBalancerIp:        "34.123.45.67",
			expectedResult:        []string{"10.0.0.1", "10.0.0.2", "192.168.1.1", "lb1.something.invalid", "lb2.something.invalid", "34.123.45.67"},
		},
		"no load balancer IP": {
			clusterIps:            []string{"10.0.0.1"},
			externalIps:           []string{"192.168.1.1"},
			loadBalancerIngresses: []string{"lb1.something.invalid"},
			loadBalancerIp:        "",
			expectedResult:        []string{"10.0.0.1", "192.168.1.1", "lb1.something.invalid"},
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
							{Hostname: "lb1.something.invalid"},
							{Hostname: "lb2.something.invalid"},
						},
					},
				},
			},
			expectedResult: &pb.KubernetesServiceData{
				// IpAddresses: []string{}, // Ignored in this test case
				Ports: []*pb.KubernetesServiceData_ServicePort{
					{
						NodePort: new(uint32(30001)),
						Port:     8080,
						Protocol: "TCP",
					},
					{
						NodePort: new(uint32(30002)),
						Port:     443,
						Protocol: "TCP",
					},
				},
				Type:              "LoadBalancer",
				ExternalName:      new(""),
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

func TestConvertToApplyObject_CiliumNetworkPolicy(t *testing.T) {
	boolTrue := true
	desc := "allow ingress"

	data := &pb.ConfiguredKubernetesObjectData{
		Id:          "cnp-1",
		Name:        "test-policy",
		Namespace:   new("default"),
		Labels:      map[string]string{"env": "prod"},
		Annotations: map[string]string{"note": "test"},
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{
						Description: &desc,
						EndpointSelector: &pb.LabelSelector{
							MatchLabels: map[string]string{"app": "web"},
						},
						EnableDefaultDeny: &pb.CiliumPolicyDefaultDeny{
							Ingress: &boolTrue,
						},
						Ingress: []*pb.CiliumPolicyIngressRule{
							{FromCidr: []string{"10.0.0.0/8"}},
						},
					},
				},
			},
		},
	}

	obj, resourceName, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	assert.Equal(t, "ciliumnetworkpolicies", resourceName)
	assert.Equal(t, "CiliumNetworkPolicy", obj.GetKind())
	assert.Equal(t, "cilium.io/v2", obj.GetAPIVersion())
	assert.Equal(t, "test-policy", obj.GetName())
	assert.Equal(t, "default", obj.GetNamespace())

	metadata, ok := obj.Object["metadata"].(map[string]any)
	require.True(t, ok)
	labels, ok := metadata["labels"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "prod", labels["env"])
	assert.Equal(t, "cnp-1", labels[CloudSecureIDLabel])
	assert.Equal(t, "cloud-operator", labels[ManagedByLabel])

	annotations, ok := metadata["annotations"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "test", annotations["note"])

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok, "expected spec to be a map")
	assert.Contains(t, spec, "endpointSelector")
	assert.Contains(t, spec, "enableDefaultDeny")
	assert.Contains(t, spec, "ingress")
	assert.Equal(t, "allow ingress", spec["description"])
}

func TestConvertToApplyObject_MultipleSpecs(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:   "cnp-multi",
		Name: "multi-spec-policy",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{EndpointSelector: &pb.LabelSelector{MatchLabels: map[string]string{"app": "web"}}},
					{EndpointSelector: &pb.LabelSelector{MatchLabels: map[string]string{"app": "api"}}},
				},
			},
		},
	}

	obj, _, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	_, hasSpec := obj.Object["spec"]
	assert.False(t, hasSpec)

	specs, hasSpecs := obj.Object["specs"]
	assert.True(t, hasSpecs)

	specsList, ok := specs.([]any)
	require.True(t, ok)
	assert.Len(t, specsList, 2)
}

func TestConvertToApplyObject_EmptySpecs(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:   "cnp-empty",
		Name: "empty-spec-policy",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{Specs: []*pb.CiliumPolicyRule{}},
		},
	}

	obj, _, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	_, hasSpec := obj.Object["spec"]
	_, hasSpecs := obj.Object["specs"]

	assert.False(t, hasSpec)
	assert.False(t, hasSpecs)
}

func TestConvertToApplyObject_CiliumClusterwideNetworkPolicy(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:   "ccnp-1",
		Name: "clusterwide-policy",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
			CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{NodeSelector: &pb.LabelSelector{MatchLabels: map[string]string{"role": "worker"}}},
				},
			},
		},
	}

	obj, resourceName, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	assert.Equal(t, "ciliumclusterwidenetworkpolicies", resourceName)
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.GetKind())
	assert.Empty(t, obj.GetNamespace())

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, spec, "nodeSelector")
}

func TestConvertToApplyObject_CiliumCIDRGroup(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:   "cidr-1",
		Name: "test-cidr-group",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumCidrGroup{
			CiliumCidrGroup: &pb.KubernetesCiliumCIDRGroupData{
				Spec: &pb.CiliumCIDRGroup{ExternalCidrs: []string{"10.0.0.0/8", "172.16.0.0/12"}},
			},
		},
	}

	obj, resourceName, err := ConvertToApplyObject(data, "cilium.io", "v2alpha1")
	require.NoError(t, err)

	assert.Equal(t, "ciliumcidrgroups", resourceName)
	assert.Equal(t, "CiliumCIDRGroup", obj.GetKind())

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)

	cidrs, ok := spec["externalCidrs"].([]any)
	require.True(t, ok)
	assert.Equal(t, "10.0.0.0/8", cidrs[0])
	assert.Equal(t, "172.16.0.0/12", cidrs[1])
}

func TestConvertToApplyObject_NilData(t *testing.T) {
	_, _, err := ConvertToApplyObject(nil, "cilium.io", "v2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestConvertToApplyObject_UnsupportedKindSpecific(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{Id: "unknown-1", Name: "unknown-kind"}
	_, _, err := ConvertToApplyObject(data, "example.io", "v1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")
}

func TestConvertToApplyObject_APIVersionFormats(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:   "cnp-api",
		Name: "api-test",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}

	tests := map[string]struct {
		apiGroup, apiVersion, expectedAPIVer string
	}{
		"with group":         {"cilium.io", "v2", "cilium.io/v2"},
		"core group (empty)": {"", "v1", "v1"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			obj, _, err := ConvertToApplyObject(data, tt.apiGroup, tt.apiVersion)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedAPIVer, obj.GetAPIVersion())
		})
	}
}

func TestConvertToApplyObject_LabelsIncludeManagementLabels(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:     "cnp-labels",
		Name:   "label-test",
		Labels: map[string]string{"custom": "value"},
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
		},
	}

	obj, _, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	metadata, ok := obj.Object["metadata"].(map[string]any)
	require.True(t, ok)
	labels, ok := metadata["labels"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "value", labels["custom"])
	assert.Equal(t, "cnp-labels", labels[CloudSecureIDLabel])
	assert.Equal(t, "cloud-operator", labels[ManagedByLabel])
	assert.Len(t, labels, 3)
}

func TestConvertToApplyObject_EmptyNamespace(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:   "ccnp-no-ns",
		Name: "cluster-scoped",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
			CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{},
		},
	}

	obj, _, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	metadata, ok := obj.Object["metadata"].(map[string]any)
	require.True(t, ok)

	_, hasNS := metadata["namespace"]
	assert.False(t, hasNS)
}

// From cilium/examples/policies/kubernetes/clusterwide/clusterscope-policy.yaml
// Selective ingress: only pods with name=luke can reach pods with name=leia.
func TestConvertToApplyObject_ClusterwideSelectiveIngress(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:   "ccnp-selective-ingress",
		Name: "selective-ingress",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
			CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{
						EndpointSelector: &pb.LabelSelector{
							MatchLabels: map[string]string{"name": "leia"},
						},
						Ingress: []*pb.CiliumPolicyIngressRule{
							{
								FromEndpoints: &pb.LabelSelectorList{
									Items: []*pb.LabelSelector{
										{MatchLabels: map[string]string{"name": "luke"}},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	obj, resourceName, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	assert.Equal(t, "cilium.io/v2", obj.Object["apiVersion"])
	assert.Equal(t, "CiliumClusterwideNetworkPolicy", obj.Object["kind"])
	assert.Equal(t, "ciliumclusterwidenetworkpolicies", resourceName)

	metadata, ok := obj.Object["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "selective-ingress", metadata["name"])
	_, hasNS := metadata["namespace"]
	assert.False(t, hasNS, "clusterwide policy should have no namespace")

	labels, ok := metadata["labels"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "ccnp-selective-ingress", labels[CloudSecureIDLabel])
	assert.Equal(t, ManagedByValue, labels[ManagedByLabel])

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)

	// endpointSelector matches name=leia
	es, ok := spec["endpointSelector"].(map[string]any)
	require.True(t, ok)
	ml, ok := es["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "leia", ml["name"])

	// ingress fromEndpoints matches name=luke
	ingress, ok := spec["ingress"].([]any)
	require.True(t, ok)
	require.Len(t, ingress, 1)
	rule, ok := ingress[0].(map[string]any)
	require.True(t, ok)

	fromEpsWrapper, ok := rule["fromEndpoints"].(map[string]any)
	require.True(t, ok)
	fromEps, ok := fromEpsWrapper["items"].([]any)
	require.True(t, ok)
	require.Len(t, fromEps, 1)
	ep, ok := fromEps[0].(map[string]any)
	require.True(t, ok)
	epLabels, ok := ep["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "luke", epLabels["name"])
}

// From cilium/examples/policies/kubernetes/health.yaml
// Health check policy: reserved:health endpoints with ingress/egress remote-node.
func TestConvertToApplyObject_CiliumHealthChecks(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:        "cnp-health",
		Name:      "health",
		Namespace: new("default"),
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{
						EndpointSelector: &pb.LabelSelector{
							MatchLabels: map[string]string{"reserved:health": ""},
						},
						Ingress: []*pb.CiliumPolicyIngressRule{
							{
								FromEntities: []string{"remote-node"},
							},
						},
						Egress: []*pb.CiliumPolicyEgressRule{
							{
								ToEntities: []string{"remote-node"},
							},
						},
					},
				},
			},
		},
	}

	obj, resourceName, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	assert.Equal(t, "cilium.io/v2", obj.Object["apiVersion"])
	assert.Equal(t, "CiliumNetworkPolicy", obj.Object["kind"])
	assert.Equal(t, "ciliumnetworkpolicies", resourceName)

	metadata, ok := obj.Object["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "health", metadata["name"])
	assert.Equal(t, "default", metadata["namespace"])

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)

	// endpointSelector matches reserved:health=""
	es, ok := spec["endpointSelector"].(map[string]any)
	require.True(t, ok)
	ml, ok := es["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, ml["reserved:health"])

	// ingress fromEntities: remote-node
	ingress, ok := spec["ingress"].([]any)
	require.True(t, ok)
	require.Len(t, ingress, 1)
	ingressRule, ok := ingress[0].(map[string]any)
	require.True(t, ok)
	fromEntities, ok := ingressRule["fromEntities"].([]any)
	require.True(t, ok)
	assert.Equal(t, []any{"remote-node"}, fromEntities)

	// egress toEntities: remote-node
	egress, ok := spec["egress"].([]any)
	require.True(t, ok)
	require.Len(t, egress, 1)
	egressRule, ok := egress[0].(map[string]any)
	require.True(t, ok)
	toEntities, ok := egressRule["toEntities"].([]any)
	require.True(t, ok)
	assert.Equal(t, []any{"remote-node"}, toEntities)
}

// From cilium/examples/policies/kubernetes/wildcard/wildcard-from-endpoints.yaml
// DNS ingress: kube-dns selector, empty fromEndpoints (wildcard), toPorts UDP 53.
func TestConvertToApplyObject_WildcardDNSIngress(t *testing.T) {
	udpProto := "UDP"
	data := &pb.ConfiguredKubernetesObjectData{
		Id:        "cnp-wildcard-dns",
		Name:      "wildcard-dns",
		Namespace: new("kube-system"),
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{
						EndpointSelector: &pb.LabelSelector{
							MatchLabels: map[string]string{
								"k8s:io.kubernetes.pod.namespace": "kube-system",
								"k8s-app":                         "kube-dns",
							},
						},
						Ingress: []*pb.CiliumPolicyIngressRule{
							{
								FromEndpoints: &pb.LabelSelectorList{
									Items: []*pb.LabelSelector{
										{}, // empty selector = wildcard
									},
								},
								ToPorts: []*pb.CiliumPolicyPortRule{
									{
										Ports: []*pb.CiliumPolicyPort{
											{Port: "53", Protocol: &udpProto},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	obj, resourceName, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	assert.Equal(t, "CiliumNetworkPolicy", obj.Object["kind"])
	assert.Equal(t, "ciliumnetworkpolicies", resourceName)

	metadata, ok := obj.Object["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "wildcard-dns", metadata["name"])
	assert.Equal(t, "kube-system", metadata["namespace"])

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)

	es, ok := spec["endpointSelector"].(map[string]any)
	require.True(t, ok)
	ml, ok := es["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "kube-system", ml["k8s:io.kubernetes.pod.namespace"])
	assert.Equal(t, "kube-dns", ml["k8s-app"])

	ingress, ok := spec["ingress"].([]any)
	require.True(t, ok)
	require.Len(t, ingress, 1)
	rule, ok := ingress[0].(map[string]any)
	require.True(t, ok)

	// fromEndpoints with empty selector (wildcard)
	fromEpsWrapper, ok := rule["fromEndpoints"].(map[string]any)
	require.True(t, ok)
	fromEps, ok := fromEpsWrapper["items"].([]any)
	require.True(t, ok)
	require.Len(t, fromEps, 1)
	// empty selector should be an empty map
	ep, ok := fromEps[0].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, ep, "wildcard selector should be empty")

	// toPorts UDP 53
	toPorts, ok := rule["toPorts"].([]any)
	require.True(t, ok)
	require.Len(t, toPorts, 1)
	portRule, ok := toPorts[0].(map[string]any)
	require.True(t, ok)
	ports, ok := portRule["ports"].([]any)
	require.True(t, ok)
	require.Len(t, ports, 1)
	p, ok := ports[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "53", p["port"])
	assert.Equal(t, "UDP", p["protocol"])
}

// From cilium/examples/policies/kubernetes/namespace-labels/namespace-labels-policy.yaml
// Namespace label selectors: faction=alliance.
func TestConvertToApplyObject_NamespaceLabelSelectors(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:        "cnp-ns-labels",
		Name:      "ns-labels-policy",
		Namespace: new("default"),
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{
						EndpointSelector: &pb.LabelSelector{
							MatchLabels: map[string]string{"name": "leia"},
						},
						Ingress: []*pb.CiliumPolicyIngressRule{
							{
								FromEndpoints: &pb.LabelSelectorList{
									Items: []*pb.LabelSelector{
										{
											MatchLabels: map[string]string{
												"name": "luke",
												"k8s:io.cilium.k8s.namespace.labels.faction": "alliance",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	obj, _, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)

	es, ok := spec["endpointSelector"].(map[string]any)
	require.True(t, ok)
	ml, ok := es["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "leia", ml["name"])

	ingress, ok := spec["ingress"].([]any)
	require.True(t, ok)
	require.Len(t, ingress, 1)
	rule, ok := ingress[0].(map[string]any)
	require.True(t, ok)

	fromEpsWrapper, ok := rule["fromEndpoints"].(map[string]any)
	require.True(t, ok)
	fromEps, ok := fromEpsWrapper["items"].([]any)
	require.True(t, ok)
	require.Len(t, fromEps, 1)
	ep, ok := fromEps[0].(map[string]any)
	require.True(t, ok)
	epLabels, ok := ep["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "luke", epLabels["name"])
	assert.Equal(t, "alliance", epLabels["k8s:io.cilium.k8s.namespace.labels.faction"])
}

// From cilium/examples/policies/kubernetes/kubedns-policy.yaml
// Egress to kube-dns: UDP 53.
func TestConvertToApplyObject_EgressToKubeDNS(t *testing.T) {
	udpProto := "UDP"
	data := &pb.ConfiguredKubernetesObjectData{
		Id:        "cnp-kubedns",
		Name:      "kubedns-policy",
		Namespace: new("default"),
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{
						EndpointSelector: &pb.LabelSelector{},
						Egress: []*pb.CiliumPolicyEgressRule{
							{
								ToEndpoints: &pb.LabelSelectorList{
									Items: []*pb.LabelSelector{
										{
											MatchLabels: map[string]string{
												"k8s:io.kubernetes.pod.namespace": "kube-system",
												"k8s-app":                         "kube-dns",
											},
										},
									},
								},
								ToPorts: []*pb.CiliumPolicyPortRule{
									{
										Ports: []*pb.CiliumPolicyPort{
											{Port: "53", Protocol: &udpProto},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	obj, _, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)

	// endpointSelector should be empty (selects all pods in namespace)
	es, ok := spec["endpointSelector"].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, es, "empty selector should select all pods")

	egress, ok := spec["egress"].([]any)
	require.True(t, ok)
	require.Len(t, egress, 1)
	rule, ok := egress[0].(map[string]any)
	require.True(t, ok)

	// toEndpoints targeting kube-dns
	toEpsWrapper, ok := rule["toEndpoints"].(map[string]any)
	require.True(t, ok)
	toEps, ok := toEpsWrapper["items"].([]any)
	require.True(t, ok)
	require.Len(t, toEps, 1)
	ep, ok := toEps[0].(map[string]any)
	require.True(t, ok)
	epLabels, ok := ep["matchLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "kube-system", epLabels["k8s:io.kubernetes.pod.namespace"])
	assert.Equal(t, "kube-dns", epLabels["k8s-app"])

	// toPorts UDP 53
	toPorts, ok := rule["toPorts"].([]any)
	require.True(t, ok)
	require.Len(t, toPorts, 1)
	portRule, ok := toPorts[0].(map[string]any)
	require.True(t, ok)
	ports, ok := portRule["ports"].([]any)
	require.True(t, ok)
	require.Len(t, ports, 1)
	p, ok := ports[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "53", p["port"])
	assert.Equal(t, "UDP", p["protocol"])
}

// From cilium/examples/policies/kubernetes/isolate-namespaces.yaml
// Namespace isolation: empty selectors restrict to same-namespace traffic.
func TestConvertToApplyObject_NamespaceIsolation(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:        "cnp-isolate-ns",
		Name:      "isolate-ns",
		Namespace: new("default"),
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{
						EndpointSelector: &pb.LabelSelector{},
						Ingress: []*pb.CiliumPolicyIngressRule{
							{
								FromEndpoints: &pb.LabelSelectorList{
									Items: []*pb.LabelSelector{
										{}, // empty = same namespace
									},
								},
							},
						},
						Egress: []*pb.CiliumPolicyEgressRule{
							{
								ToEndpoints: &pb.LabelSelectorList{
									Items: []*pb.LabelSelector{
										{}, // empty = same namespace
									},
								},
							},
						},
					},
				},
			},
		},
	}

	obj, resourceName, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	assert.Equal(t, "CiliumNetworkPolicy", obj.Object["kind"])
	assert.Equal(t, "ciliumnetworkpolicies", resourceName)

	metadata, ok := obj.Object["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "isolate-ns", metadata["name"])
	assert.Equal(t, "default", metadata["namespace"])

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)

	// empty endpointSelector = all pods in namespace
	es, ok := spec["endpointSelector"].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, es)

	// ingress: fromEndpoints with empty selector
	ingress, ok := spec["ingress"].([]any)
	require.True(t, ok)
	require.Len(t, ingress, 1)
	ingressRule, ok := ingress[0].(map[string]any)
	require.True(t, ok)
	fromEpsWrapper, ok := ingressRule["fromEndpoints"].(map[string]any)
	require.True(t, ok)
	fromEps, ok := fromEpsWrapper["items"].([]any)
	require.True(t, ok)
	require.Len(t, fromEps, 1)
	fromEp, ok := fromEps[0].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, fromEp, "empty selector = same namespace")

	// egress: toEndpoints with empty selector
	egress, ok := spec["egress"].([]any)
	require.True(t, ok)
	require.Len(t, egress, 1)
	egressRule, ok := egress[0].(map[string]any)
	require.True(t, ok)
	toEpsWrapper, ok := egressRule["toEndpoints"].(map[string]any)
	require.True(t, ok)
	toEps, ok := toEpsWrapper["items"].([]any)
	require.True(t, ok)
	require.Len(t, toEps, 1)
	toEp, ok := toEps[0].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, toEp, "empty selector = same namespace")
}

// Verify that nil annotations marshal as "annotations":null in the JSON sent to the API server,
// so SSA reclaims ownership and clears any annotations not set by CloudSecure.
func TestConvertToApplyObject_NilAnnotationsMarshalAsNull(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:   "cnp-no-annotations",
		Name: "no-annotations",
		// Annotations intentionally not set (nil)
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{EndpointSelector: &pb.LabelSelector{MatchLabels: map[string]string{"app": "test"}}},
				},
			},
		},
	}

	obj, _, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	metadata, ok := obj.Object["metadata"].(map[string]any)
	require.True(t, ok)

	// annotations key must be present in the map (not omitted)
	val, exists := metadata["annotations"]
	assert.True(t, exists, "annotations key must be present so SSA claims ownership")
	assert.Nil(t, val, "nil annotations should remain nil, not an empty map")
}

func TestConvertToApplyObject_EmitUnpopulatedFalse(t *testing.T) {
	data := &pb.ConfiguredKubernetesObjectData{
		Id:   "cnp-sparse",
		Name: "sparse-policy",
		KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
			CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{
				Specs: []*pb.CiliumPolicyRule{
					{EndpointSelector: &pb.LabelSelector{MatchLabels: map[string]string{"app": "minimal"}}},
				},
			},
		},
	}

	obj, _, err := ConvertToApplyObject(data, "cilium.io", "v2")
	require.NoError(t, err)

	spec, ok := obj.Object["spec"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, spec, "endpointSelector")
	assert.NotContains(t, spec, "description")
	assert.NotContains(t, spec, "nodeSelector")
	assert.NotContains(t, spec, "enableDefaultDeny")
	assert.NotContains(t, spec, "ingress")
	assert.NotContains(t, spec, "egress")
}
func TestCopyLabels(t *testing.T) {
	tests := map[string]struct {
		labels   map[string]string
		id       string
		expected map[string]string
	}{
		"with existing labels": {
			labels: map[string]string{"env": "prod", "team": "platform"},
			id:     "obj-1",
			expected: map[string]string{
				"env": "prod", "team": "platform",
				CloudSecureIDLabel: "obj-1", ManagedByLabel: ManagedByValue,
			},
		},
		"nil labels": {
			labels:   nil,
			id:       "obj-2",
			expected: map[string]string{CloudSecureIDLabel: "obj-2", ManagedByLabel: ManagedByValue},
		},
		"empty labels": {
			labels:   map[string]string{},
			id:       "obj-3",
			expected: map[string]string{CloudSecureIDLabel: "obj-3", ManagedByLabel: ManagedByValue},
		},
		"conflicting management labels are overwritten": {
			labels:   map[string]string{CloudSecureIDLabel: "attacker", ManagedByLabel: "someone-else"},
			id:       "obj-4",
			expected: map[string]string{CloudSecureIDLabel: "obj-4", ManagedByLabel: ManagedByValue},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := copyLabels(tt.labels, tt.id)
			assert.Equal(t, tt.expected, result)
		})
	}
}
