package controller

import (
	"context"
	"testing"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// MockDynamicInterface mocks the dynamic.Interface
type MockDynamicInterface struct {
	mock.Mock
}

func (m *MockDynamicInterface) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	args := m.Called(resource)
	return args.Get(0).(dynamic.NamespaceableResourceInterface)
}

// MockNamespaceableResourceInterface mocks the dynamic.NamespaceableResourceInterface
type MockNamespaceableResourceInterface struct {
	mock.Mock
}

func (m *MockNamespaceableResourceInterface) Namespace(namespace string) dynamic.ResourceInterface {
	args := m.Called(namespace)
	return args.Get(0).(dynamic.ResourceInterface)
}

func (m *MockNamespaceableResourceInterface) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	args := m.Called(ctx, opts)
	if list, ok := args.Get(0).(*unstructured.UnstructuredList); ok {
		return list, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockNamespaceableResourceInterface) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(ctx, opts)
	if watcher, ok := args.Get(0).(watch.Interface); ok {
		return watcher, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockNamespaceableResourceInterface) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	args := m.Called(ctx, name, obj, opts, subresources)
	if result, ok := args.Get(0).(*unstructured.Unstructured); ok {
		return result, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockNamespaceableResourceInterface) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	args := m.Called(ctx, name, obj, opts)
	if result, ok := args.Get(0).(*unstructured.Unstructured); ok {
		return result, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockNamespaceableResourceInterface) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	args := m.Called(ctx, obj, opts, subresources)
	if result, ok := args.Get(0).(*unstructured.Unstructured); ok {
		return result, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockNamespaceableResourceInterface) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	args := m.Called(ctx, obj, opts, subresources)
	if result, ok := args.Get(0).(*unstructured.Unstructured); ok {
		return result, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockNamespaceableResourceInterface) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	args := m.Called(ctx, obj, opts)
	if result, ok := args.Get(0).(*unstructured.Unstructured); ok {
		return result, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockNamespaceableResourceInterface) Delete(ctx context.Context, name string, opts metav1.DeleteOptions, subresources ...string) error {
	args := m.Called(ctx, name, opts, subresources)
	return args.Error(0)
}

func (m *MockNamespaceableResourceInterface) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	args := m.Called(ctx, opts, listOpts)
	return args.Error(0)
}

func (m *MockNamespaceableResourceInterface) Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	args := m.Called(ctx, name, opts, subresources)
	if result, ok := args.Get(0).(*unstructured.Unstructured); ok {
		return result, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockNamespaceableResourceInterface) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	args := m.Called(ctx, name, pt, data, opts, subresources)
	if result, ok := args.Get(0).(*unstructured.Unstructured); ok {
		return result, args.Error(1)
	}
	return nil, args.Error(1)
}

// MockResourceInterface mocks the dynamic.ResourceInterface
type MockResourceInterface struct {
	mock.Mock
}

func (m *MockResourceInterface) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	args := m.Called(ctx, opts)
	if list, ok := args.Get(0).(*unstructured.UnstructuredList); ok {
		return list, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockResourceInterface) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	args := m.Called(ctx, opts)
	if watcher, ok := args.Get(0).(watch.Interface); ok {
		return watcher, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockResourceInterface) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	args := m.Called(ctx, name, obj, opts, subresources)
	if result, ok := args.Get(0).(*unstructured.Unstructured); ok {
		return result, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockResourceInterface) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, opts metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	args := m.Called(ctx, name, obj, opts)
	if result, ok := args.Get(0).(*unstructured.Unstructured); ok {
		return result, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockResourceInterface) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	args := m.Called(ctx, obj, opts, subresources)
	if result, ok := args.Get(0).(*unstructured.Unstructured); ok {
		return result, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockResourceInterface) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	args := m.Called(ctx, obj, opts, subresources)
	if result, ok := args.Get(0).(*unstructured.Unstructured); ok {
		return result, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockResourceInterface) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	args := m.Called(ctx, obj, opts)
	if result, ok := args.Get(0).(*unstructured.Unstructured); ok {
		return result, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockResourceInterface) Delete(ctx context.Context, name string, opts metav1.DeleteOptions, subresources ...string) error {
	args := m.Called(ctx, name, opts, subresources)
	return args.Error(0)
}

func (m *MockResourceInterface) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	args := m.Called(ctx, opts, listOpts)
	return args.Error(0)
}

func (m *MockResourceInterface) Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	args := m.Called(ctx, name, opts, subresources)
	if result, ok := args.Get(0).(*unstructured.Unstructured); ok {
		return result, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockResourceInterface) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	args := m.Called(ctx, name, pt, data, opts, subresources)
	if result, ok := args.Get(0).(*unstructured.Unstructured); ok {
		return result, args.Error(1)
	}
	return nil, args.Error(1)
}

// MockWatchInterface mocks the watch.Interface
type MockWatchInterface struct {
	mock.Mock
}

func (m *MockWatchInterface) Stop() {
	m.Called()
}

func (m *MockWatchInterface) ResultChan() <-chan watch.Event {
	args := m.Called()
	return args.Get(0).(<-chan watch.Event)
}

// MockKubernetesResourceStream mocks the resource stream interface
type mockKubernetesResourceStream struct {
	grpc.ClientStream
	events []*pb.SendKubernetesResourcesRequest
}

func (m *mockKubernetesResourceStream) Send(req *pb.SendKubernetesResourcesRequest) error {
	m.events = append(m.events, req)
	return nil
}

func (m *mockKubernetesResourceStream) Recv() (*pb.SendKubernetesResourcesResponse, error) {
	return &pb.SendKubernetesResourcesResponse{}, nil
}

func (m *mockKubernetesResourceStream) CloseSend() error {
	return nil
}

func (m *mockKubernetesResourceStream) Context() context.Context {
	return context.Background()
}

func (m *mockKubernetesResourceStream) Header() (metadata.MD, error) {
	return nil, nil
}

func (m *mockKubernetesResourceStream) Trailer() metadata.MD {
	return nil
}

func (m *mockKubernetesResourceStream) CloseAndRecv() (*pb.SendKubernetesResourcesResponse, error) {
	return &pb.SendKubernetesResourcesResponse{}, nil
}

// ResourceManagerTestSuite is a test suite for ResourceManager
type ResourceManagerTestSuite struct {
	suite.Suite
	resourceManager *ResourceManager
	mockDynamic     *MockDynamicInterface
	mockResource    *MockResourceInterface
	mockClientset   *kubernetes.Clientset
	logger          *zap.Logger
}

func TestResourceManagerTestSuite(t *testing.T) {
	suite.Run(t, new(ResourceManagerTestSuite))
}

func (suite *ResourceManagerTestSuite) SetupTest() {
	suite.mockDynamic = new(MockDynamicInterface)
	suite.mockResource = new(MockResourceInterface)
	suite.mockClientset = &kubernetes.Clientset{}
	suite.logger = zap.NewNop()

	suite.resourceManager = &ResourceManager{
		clientset:     suite.mockClientset,
		logger:        suite.logger,
		dynamicClient: suite.mockDynamic,
		streamManager: &streamManager{
			bufferedGrpcSyncer: &BufferedGrpcWriteSyncer{},
			streamClient: &streamClient{
				resourceStream: &mockKubernetesResourceStream{},
			},
		},
	}
}

func (suite *ResourceManagerTestSuite) TestFetchResources() {
	tests := []struct {
		name          string
		resource      schema.GroupVersionResource
		namespace     string
		mockList      *unstructured.UnstructuredList
		mockError     error
		expectedError bool
	}{
		{
			name:      "successful fetch",
			resource:  schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
			namespace: "default",
			mockList: &unstructured.UnstructuredList{
				Items: []unstructured.Unstructured{
					{
						Object: map[string]interface{}{
							"metadata": map[string]interface{}{
								"name": "test-pod",
							},
						},
					},
				},
			},
			mockError:     nil,
			expectedError: false,
		},
		{
			name:          "fetch error",
			resource:      schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
			namespace:     "default",
			mockList:      nil,
			mockError:     assert.AnError,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		suite.Run(tt.name, func() {
			// Reset mock expectations
			suite.mockDynamic.ExpectedCalls = nil
			suite.mockResource.ExpectedCalls = nil

			mockNamespaceable := new(MockNamespaceableResourceInterface)
			suite.mockDynamic.On("Resource", tt.resource).Return(mockNamespaceable)
			mockNamespaceable.On("Namespace", tt.namespace).Return(suite.mockResource)
			suite.mockResource.On("List", context.Background(), metav1.ListOptions{}).Return(tt.mockList, tt.mockError)

			result, err := suite.resourceManager.FetchResources(context.Background(), tt.resource, tt.namespace)

			if tt.expectedError {
				assert.Error(suite.T(), err)
				assert.Nil(suite.T(), result)
			} else {
				assert.NoError(suite.T(), err)
				assert.Equal(suite.T(), tt.mockList, result)
			}

			// Verify all expected calls were made
			suite.mockDynamic.AssertExpectations(suite.T())
			suite.mockResource.AssertExpectations(suite.T())
		})
	}
}

func (suite *ResourceManagerTestSuite) TestExtractObjectMetas() {
	tests := []struct {
		name          string
		resources     *unstructured.UnstructuredList
		expectedMetas []metav1.ObjectMeta
		expectedError bool
	}{
		{
			name: "successful extraction",
			resources: &unstructured.UnstructuredList{
				Items: []unstructured.Unstructured{
					{
						Object: map[string]interface{}{
							"metadata": map[string]interface{}{
								"name":      "test-pod",
								"namespace": "default",
							},
						},
					},
				},
			},
			expectedMetas: []metav1.ObjectMeta{
				{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			expectedError: false,
		},
		{
			name: "empty list",
			resources: &unstructured.UnstructuredList{
				Items: []unstructured.Unstructured{},
			},
			expectedMetas: []metav1.ObjectMeta{},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		suite.Run(tt.name, func() {
			result, err := suite.resourceManager.ExtractObjectMetas(tt.resources)

			if tt.expectedError {
				assert.Error(suite.T(), err)
				assert.Nil(suite.T(), result)
			} else {
				assert.NoError(suite.T(), err)
				assert.Equal(suite.T(), tt.expectedMetas, result)
			}
		})
	}
}

func (suite *ResourceManagerTestSuite) TestDynamicListResources() {
	// Create a mock resource
	mockResource := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]interface{}{
				"name":            "test-pod",
				"namespace":       "default",
				"resourceVersion": "1",
			},
		},
	}

	// Create a mock list response
	mockList := &unstructured.UnstructuredList{
		Items: []unstructured.Unstructured{*mockResource},
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"resourceVersion": "1",
			},
		},
	}

	// Set up the mock hierarchy
	mockNamespaceable := new(MockNamespaceableResourceInterface)
	suite.mockDynamic.On("Resource", mock.Anything).Return(mockNamespaceable)
	mockNamespaceable.On("Namespace", mock.Anything).Return(suite.mockResource)
	suite.mockResource.On("List", mock.Anything, mock.Anything).Return(mockList, nil)

	// Call DynamicListResources
	version, err := suite.resourceManager.DynamicListResources(context.Background(), suite.logger, "pods", "")

	// Verify the results
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), "1", version)

	// Verify all expected calls were made
	suite.mockDynamic.AssertExpectations(suite.T())
	suite.mockResource.AssertExpectations(suite.T())
}

//func (suite *ResourceManagerTestSuite) TestWatchEvents() {
//	tests := []struct {
//		name          string
//		resource      string
//		apiGroup      string
//		watchOptions  metav1.ListOptions
//		mockEvents    []watch.Event
//		mockError     error
//		expectedError bool
//	}{
//		{
//			name:     "successful watch",
//			resource: "pods",
//			apiGroup: "",
//			watchOptions: metav1.ListOptions{
//				Watch:           true,
//				ResourceVersion: "12345",
//			},
//			mockEvents: []watch.Event{
//				{
//					Type: watch.Added,
//					Object: &unstructured.Unstructured{
//						Object: map[string]interface{}{
//							"metadata": map[string]interface{}{
//								"name":      "test-pod",
//								"namespace": "default",
//							},
//						},
//					},
//				},
//			},
//			mockError:     nil,
//			expectedError: false,
//		},
//		{
//			name:     "watch error",
//			resource: "pods",
//			apiGroup: "",
//			watchOptions: metav1.ListOptions{
//				Watch:           true,
//				ResourceVersion: "12345",
//			},
//			mockEvents:    nil,
//			mockError:     assert.AnError,
//			expectedError: true,
//		},
//	}
//
//	for _, tt := range tests {
//		suite.Run(tt.name, func() {
//			// Reset mock expectations
//			suite.mockDynamic.ExpectedCalls = nil
//			suite.mockResource.ExpectedCalls = nil
//
//			mockNamespaceable := new(MockNamespaceableResourceInterface)
//			mockWatch := new(MockWatchInterface)
//			eventChan := make(chan watch.Event, len(tt.mockEvents))
//
//			suite.mockDynamic.On("Resource", schema.GroupVersionResource{
//				Group:    tt.apiGroup,
//				Version:  "v1",
//				Resource: tt.resource,
//			}).Return(mockNamespaceable)
//			mockNamespaceable.On("Namespace", metav1.NamespaceAll).Return(suite.mockResource)
//			suite.mockResource.On("Watch", mock.Anything, tt.watchOptions).Return(mockWatch, tt.mockError)
//
//			if tt.mockError == nil {
//				mockWatch.On("ResultChan").Return((<-chan watch.Event)(eventChan))
//				mockWatch.On("Stop").Return()
//
//				// Create a context with timeout
//				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
//				defer cancel()
//
//				// Send mock events in a goroutine
//				go func() {
//					defer close(eventChan)
//					for _, event := range tt.mockEvents {
//						select {
//						case eventChan <- event:
//						case <-ctx.Done():
//							return
//						}
//					}
//				}()
//
//				// Call watchEvents with the timeout context
//				err := suite.resourceManager.watchEvents(ctx, tt.resource, tt.apiGroup, tt.watchOptions)
//				assert.NoError(suite.T(), err)
//			} else {
//				err := suite.resourceManager.watchEvents(context.Background(), tt.resource, tt.apiGroup, tt.watchOptions)
//				assert.Error(suite.T(), err)
//			}
//
//			// Verify all expected calls were made
//			suite.mockDynamic.AssertExpectations(suite.T())
//			suite.mockResource.AssertExpectations(suite.T())
//			mockWatch.AssertExpectations(suite.T())
//		})
//	}
//}

func (suite *ResourceManagerTestSuite) TestRateLimiting() {
	tests := []struct {
		name          string
		ctx           context.Context
		expectedError bool
	}{
		{
			name:          "successful rate limit",
			ctx:           context.Background(),
			expectedError: false,
		},
		{
			name: "context cancelled",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			}(),
			expectedError: true,
		},
	}

	for _, tt := range tests {
		suite.Run(tt.name, func() {
			limiter := rate.NewLimiter(1, 5)
			err := limiter.Wait(tt.ctx)

			if tt.expectedError {
				assert.Error(suite.T(), err)
			} else {
				assert.NoError(suite.T(), err)
			}
		})
	}
}
