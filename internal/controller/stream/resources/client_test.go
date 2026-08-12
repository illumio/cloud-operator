// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/controller/stream/config/cache"
	"github.com/illumio/cloud-operator/internal/convert"
)

// mockResourcesStream mocks the stream.KubernetesResourcesStream interface.
type mockResourcesStream struct {
	mock.Mock
}

func (m *mockResourcesStream) Send(req *pb.SendKubernetesResourcesRequest) error {
	args := m.Called(req)

	return args.Error(0)
}

func (m *mockResourcesStream) Recv() (*pb.SendKubernetesResourcesResponse, error) {
	args := m.Called()
	if resp, ok := args.Get(0).(*pb.SendKubernetesResourcesResponse); ok {
		return resp, args.Error(1)
	}

	return nil, args.Error(1)
}

// ResourcesClientTestSuite tests the resourcesClient.
type ResourcesClientTestSuite struct {
	suite.Suite

	mockStream *mockResourcesStream
	stats      *stream.Stats
	logger     *zap.Logger
	client     *resourcesClient
}

func TestResourcesClientTestSuite(t *testing.T) {
	suite.Run(t, new(ResourcesClientTestSuite))
}

func (s *ResourcesClientTestSuite) SetupTest() {
	s.mockStream = &mockResourcesStream{}
	s.logger = zap.NewNop()
	s.stats = stream.NewStats()
	s.client = &resourcesClient{
		grpcStream:    s.mockStream,
		logger:        s.logger,
		stats:         s.stats,
		flowCollector: pb.FlowCollector_FLOW_COLLECTOR_CILIUM,
		runtimeCache:  cache.NewConfiguredObjectCache(),
	}
}

func (s *ResourcesClientTestSuite) TestClusterName_Empty() {
	// When clusterName is empty, it should remain empty
	client := &resourcesClient{
		grpcStream:    s.mockStream,
		logger:        s.logger,
		stats:         s.stats,
		flowCollector: pb.FlowCollector_FLOW_COLLECTOR_CILIUM,
		clusterName:   "",
	}

	s.Empty(client.clusterName)
}

func (s *ResourcesClientTestSuite) TestClusterName_Set() {
	// When clusterName is set, it should be stored in the client
	expectedName := "my-self-managed-cluster"
	client := &resourcesClient{
		grpcStream:    s.mockStream,
		logger:        s.logger,
		stats:         s.stats,
		flowCollector: pb.FlowCollector_FLOW_COLLECTOR_CILIUM,
		clusterName:   expectedName,
	}

	s.Equal(expectedName, client.clusterName)
}

func (s *ResourcesClientTestSuite) TestSendKeepalive_Success() {
	s.mockStream.On("Send", mock.MatchedBy(func(req *pb.SendKubernetesResourcesRequest) bool {
		return req.GetKeepalive() != nil
	})).Return(nil).Once()

	err := s.client.SendKeepalive(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())
}

func (s *ResourcesClientTestSuite) TestSendKeepalive_Error() {
	expectedErr := errors.New("send error")
	s.mockStream.On("Send", mock.Anything).Return(expectedErr).Once()

	err := s.client.SendKeepalive(context.Background())

	s.Require().ErrorIs(err, expectedErr)
	s.mockStream.AssertExpectations(s.T())
}

func (s *ResourcesClientTestSuite) TestSendKeepalive_AfterClose() {
	err := s.client.Close()
	s.Require().NoError(err)

	err = s.client.SendKeepalive(context.Background())

	s.Require().Error(err)
	s.Contains(err.Error(), "stream closed")
}

func (s *ResourcesClientTestSuite) TestRuntimeCacheHandler_ExcludesUnownedCNP() {
	pendingSnapshot := make(map[string]*pb.ConfiguredKubernetesObjectData)
	handler := s.client.newRuntimeCacheHandler(pendingSnapshot)

	// A CNP the operator does not own (no operator field-manager entry) and is not
	// already tracked must not enter the runtime cache.
	obj := &unstructured.Unstructured{}
	obj.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "kubectl"}})

	unownedCNP := &pb.KubernetesObjectData{
		Name: "illumio-cloud-operator-flow-logging",
		Kind: "ClusterNetworkPolicy",
		KindSpecific: &pb.KubernetesObjectData_AwsClusterNetworkPolicy{
			AwsClusterNetworkPolicy: &pb.KubernetesAWSClusterNetworkPolicyData{Tier: "Baseline"},
		},
	}

	err := handler(context.Background(), "", obj, unownedCNP)
	s.Require().NoError(err)
	s.Empty(pendingSnapshot, "a CNP the operator does not own must not enter the runtime cache")
}

func (s *ResourcesClientTestSuite) TestRuntimeCacheHandler_IncludesManagedCNP() {
	pendingSnapshot := make(map[string]*pb.ConfiguredKubernetesObjectData)
	handler := s.client.newRuntimeCacheHandler(pendingSnapshot)

	// A CNP the operator owns (it SSA-applies the main resource under its field
	// manager) must enter the runtime cache so the reconciler can track and reconcile it.
	obj := &unstructured.Unstructured{}
	obj.SetManagedFields([]metav1.ManagedFieldsEntry{{
		Manager:   convert.FieldManager,
		Operation: metav1.ManagedFieldsOperationApply,
	}})

	managedCNP := &pb.KubernetesObjectData{
		Name: "cloudsecure-policy",
		Kind: "ClusterNetworkPolicy",
		KindSpecific: &pb.KubernetesObjectData_AwsClusterNetworkPolicy{
			AwsClusterNetworkPolicy: &pb.KubernetesAWSClusterNetworkPolicyData{Tier: "Admin"},
		},
	}

	err := handler(context.Background(), "", obj, managedCNP)
	s.Require().NoError(err)
	s.Len(pendingSnapshot, 1, "a CNP the operator owns must enter the runtime cache")
	s.Contains(pendingSnapshot, convert.CacheKey("ClusterNetworkPolicy", "", "cloudsecure-policy"))
}

func (s *ResourcesClientTestSuite) TestClose() {
	err := s.client.Close()

	s.Require().NoError(err)
	s.True(s.client.closed)
}

func (s *ResourcesClientTestSuite) TestClose_Idempotent() {
	err := s.client.Close()
	s.Require().NoError(err)

	err = s.client.Close()
	s.Require().NoError(err)

	s.True(s.client.closed)
}

func (s *ResourcesClientTestSuite) TestSendToStream_Success() {
	request := &pb.SendKubernetesResourcesRequest{
		Request: &pb.SendKubernetesResourcesRequest_Keepalive{
			Keepalive: &pb.Keepalive{},
		},
	}
	s.mockStream.On("Send", request).Return(nil).Once()

	err := s.client.SendToStream(request)

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())
}

func (s *ResourcesClientTestSuite) TestSendToStream_Error() {
	request := &pb.SendKubernetesResourcesRequest{
		Request: &pb.SendKubernetesResourcesRequest_Keepalive{
			Keepalive: &pb.Keepalive{},
		},
	}
	expectedErr := errors.New("send error")
	s.mockStream.On("Send", request).Return(expectedErr).Once()

	err := s.client.SendToStream(request)

	s.Require().ErrorIs(err, expectedErr)
	s.mockStream.AssertExpectations(s.T())
}

func (s *ResourcesClientTestSuite) TestSendToStream_AfterClose() {
	err := s.client.Close()
	s.Require().NoError(err)

	request := &pb.SendKubernetesResourcesRequest{
		Request: &pb.SendKubernetesResourcesRequest_Keepalive{
			Keepalive: &pb.Keepalive{},
		},
	}

	err = s.client.SendToStream(request)

	s.Require().Error(err)
	s.Contains(err.Error(), "stream closed")
}

func (s *ResourcesClientTestSuite) TestSendObjectData_Success() {
	metadata := &pb.KubernetesObjectData{
		Kind: "Pod",
		Name: "test-pod",
	}

	s.mockStream.On("Send", mock.MatchedBy(func(req *pb.SendKubernetesResourcesRequest) bool {
		return req.GetResourceData() != nil && req.GetResourceData().GetKind() == "Pod"
	})).Return(nil).Once()

	err := s.client.SendObjectData(s.logger, metadata)

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())
}

func (s *ResourcesClientTestSuite) TestSendObjectData_Error() {
	metadata := &pb.KubernetesObjectData{
		Kind: "Pod",
		Name: "test-pod",
	}
	expectedErr := errors.New("send error")
	s.mockStream.On("Send", mock.Anything).Return(expectedErr).Once()

	err := s.client.SendObjectData(s.logger, metadata)

	s.Require().ErrorIs(err, expectedErr)
	s.mockStream.AssertExpectations(s.T())
}

func (s *ResourcesClientTestSuite) TestCreateMutationObject_Added() {
	metadata := &pb.KubernetesObjectData{
		Kind: "Pod",
		Name: "test-pod",
	}

	mutation := s.client.CreateMutationObject(metadata, watch.Added)

	s.Require().NotNil(mutation)
	s.Require().NotNil(mutation.GetCreateResource())
	s.Equal("Pod", mutation.GetCreateResource().GetKind())
}

func (s *ResourcesClientTestSuite) TestCreateMutationObject_Modified() {
	metadata := &pb.KubernetesObjectData{
		Kind: "Pod",
		Name: "test-pod",
	}

	mutation := s.client.CreateMutationObject(metadata, watch.Modified)

	s.Require().NotNil(mutation)
	s.Require().NotNil(mutation.GetUpdateResource())
	s.Equal("Pod", mutation.GetUpdateResource().GetKind())
}

func (s *ResourcesClientTestSuite) TestCreateMutationObject_Deleted() {
	metadata := &pb.KubernetesObjectData{
		Kind: "Pod",
		Name: "test-pod",
	}

	mutation := s.client.CreateMutationObject(metadata, watch.Deleted)

	s.Require().NotNil(mutation)
	s.Require().NotNil(mutation.GetDeleteResource())
	s.Equal("Pod", mutation.GetDeleteResource().GetKind())
}

func (s *ResourcesClientTestSuite) TestCreateMutationObject_Bookmark() {
	metadata := &pb.KubernetesObjectData{
		Kind: "Pod",
		Name: "test-pod",
	}

	mutation := s.client.CreateMutationObject(metadata, watch.Bookmark)

	s.Nil(mutation)
}

func (s *ResourcesClientTestSuite) TestCreateMutationObject_Error() {
	metadata := &pb.KubernetesObjectData{
		Kind: "Pod",
		Name: "test-pod",
	}

	mutation := s.client.CreateMutationObject(metadata, watch.Error)

	s.Nil(mutation)
}

func TestHasFieldManager(t *testing.T) {
	const op = "illumio-cloud-operator"

	apply := func(manager string) metav1.ManagedFieldsEntry {
		return metav1.ManagedFieldsEntry{Manager: manager, Operation: metav1.ManagedFieldsOperationApply}
	}

	tests := []struct {
		name   string
		fields []metav1.ManagedFieldsEntry
		check  string
		want   bool
	}{
		{"apply present among multiple", []metav1.ManagedFieldsEntry{apply(op), apply("kubectl")}, op, true},
		{"sole apply manager", []metav1.ManagedFieldsEntry{apply(op)}, op, true},
		{"absent", []metav1.ManagedFieldsEntry{apply("kubectl"), apply("helm")}, op, false},
		{"empty managed fields", nil, op, false},
		{
			"manager present but only Update op",
			[]metav1.ManagedFieldsEntry{{Manager: op, Operation: metav1.ManagedFieldsOperationUpdate}},
			op, false,
		},
		{
			"manager present but on subresource",
			[]metav1.ManagedFieldsEntry{{Manager: op, Operation: metav1.ManagedFieldsOperationApply, Subresource: "status"}},
			op, false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &unstructured.Unstructured{}
			obj.SetManagedFields(tt.fields)

			assert.Equal(t, tt.want, hasFieldManager(obj, tt.check))
		})
	}
}
