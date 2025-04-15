package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

type mockKubernetesInfoServiceClient struct {
	pb.KubernetesInfoServiceClient
	sendLogsClient                   *mockSendLogsClient
	sendNetworkFlowsClient           *mockSendNetworkFlowsClient
	sendKubernetesResourcesClient    *mockSendKubernetesResourcesClient
	getConfigurationUpdatesClient    *mockGetConfigurationUpdatesClient
	clusterMetadataResponse          *pb.SendKubernetesResourcesResponse
	resourceSnapshotCompleteResponse *pb.SendKubernetesResourcesResponse
	clusterMetadataError             error
	resourceSnapshotCompleteError    error
}

type mockSendLogsClient struct {
	grpc.ClientStream
	recvError error
	recvCount int
}

func (m *mockSendLogsClient) Recv() (*pb.SendLogsResponse, error) {
	m.recvCount++
	if m.recvError != nil {
		return nil, m.recvError
	}
	return &pb.SendLogsResponse{}, nil
}

func (m *mockSendLogsClient) Send(req *pb.SendLogsRequest) error {
	return nil
}

func (m *mockSendLogsClient) CloseSend() error {
	return nil
}

type mockSendNetworkFlowsClient struct {
	grpc.ClientStream
	sendError   error
	lastRequest *pb.SendKubernetesNetworkFlowsRequest
}

func (m *mockSendNetworkFlowsClient) Send(req *pb.SendKubernetesNetworkFlowsRequest) error {
	m.lastRequest = req
	return m.sendError
}

func (m *mockSendNetworkFlowsClient) Recv() (*pb.SendKubernetesNetworkFlowsResponse, error) {
	return &pb.SendKubernetesNetworkFlowsResponse{}, nil
}

func (m *mockSendNetworkFlowsClient) CloseSend() error {
	return nil
}

type mockSendKubernetesResourcesClient struct {
	grpc.ClientStream
	sendError   error
	lastRequest *pb.SendKubernetesResourcesRequest
}

func (m *mockSendKubernetesResourcesClient) Send(req *pb.SendKubernetesResourcesRequest) error {
	m.lastRequest = req
	return m.sendError
}

func (m *mockSendKubernetesResourcesClient) Recv() (*pb.SendKubernetesResourcesResponse, error) {
	return &pb.SendKubernetesResourcesResponse{}, nil
}

func (m *mockSendKubernetesResourcesClient) CloseSend() error {
	return nil
}

type mockGetConfigurationUpdatesClient struct {
	grpc.ClientStream
	recvError error
	recvCount int
}

func (m *mockGetConfigurationUpdatesClient) Recv() (*pb.GetConfigurationUpdatesResponse, error) {
	m.recvCount++
	if m.recvError != nil {
		return nil, m.recvError
	}
	return &pb.GetConfigurationUpdatesResponse{}, nil
}

func (m *mockGetConfigurationUpdatesClient) Send(req *pb.GetConfigurationUpdatesRequest) error {
	return nil
}

func (m *mockGetConfigurationUpdatesClient) CloseSend() error {
	return nil
}

func (m *mockKubernetesInfoServiceClient) SendLogs(ctx context.Context, opts ...grpc.CallOption) (pb.KubernetesInfoService_SendLogsClient, error) {
	return m.sendLogsClient, nil
}

func (m *mockKubernetesInfoServiceClient) SendKubernetesNetworkFlows(ctx context.Context, opts ...grpc.CallOption) (pb.KubernetesInfoService_SendKubernetesNetworkFlowsClient, error) {
	return m.sendNetworkFlowsClient, nil
}

func (m *mockKubernetesInfoServiceClient) SendKubernetesResources(ctx context.Context, opts ...grpc.CallOption) (pb.KubernetesInfoService_SendKubernetesResourcesClient, error) {
	return m.sendKubernetesResourcesClient, nil
}

func (m *mockKubernetesInfoServiceClient) GetConfigurationUpdates(ctx context.Context, opts ...grpc.CallOption) (pb.KubernetesInfoService_GetConfigurationUpdatesClient, error) {
	return m.getConfigurationUpdatesClient, nil
}

func (m *mockKubernetesInfoServiceClient) SendClusterMetadata(ctx context.Context, in *pb.SendKubernetesResourcesRequest, opts ...grpc.CallOption) (*pb.SendKubernetesResourcesResponse, error) {
	return m.clusterMetadataResponse, m.clusterMetadataError
}

func (m *mockKubernetesInfoServiceClient) SendResourceSnapshotComplete(ctx context.Context, in *pb.SendKubernetesResourcesRequest, opts ...grpc.CallOption) (*pb.SendKubernetesResourcesResponse, error) {
	return m.resourceSnapshotCompleteResponse, m.resourceSnapshotCompleteError
}

// setupMockKubernetesEnv creates a mock Kubernetes environment for testing.
// It returns a cleanup function that should be called using defer.
func setupMockKubernetesEnv(t *testing.T) func() {
	// Create a temporary directory for the service account token
	tmpDir, err := os.MkdirTemp("", "test-k8s-*")
	if err != nil {
		t.Fatal(err)
	}

	// Create the service account token directory structure
	tokenDir := filepath.Join(tmpDir, "var", "run", "secrets", "kubernetes.io", "serviceaccount")
	err = os.MkdirAll(tokenDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	// Create a dummy token file
	tokenFile := filepath.Join(tokenDir, "token")
	err = os.WriteFile(tokenFile, []byte("dummy-token"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Create a dummy CA certificate file
	caFile := filepath.Join(tokenDir, "ca.crt")
	err = os.WriteFile(caFile, []byte("dummy-ca-cert"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Set up environment variables for in-cluster config
	t.Setenv("KUBERNETES_SERVICE_HOST", "localhost")
	t.Setenv("KUBERNETES_SERVICE_PORT", "8080")
	t.Setenv("KUBERNETES_SERVICE_TOKEN_FILE", tokenFile)

	return func() {
		os.RemoveAll(tmpDir)
	}
}

func TestServerIsHealthy(t *testing.T) {
	t.Run("healthy server", func(t *testing.T) {
		dd.processingResources = false
		assert.True(t, ServerIsHealthy())
	})

	t.Run("deadlock detected", func(t *testing.T) {
		dd.processingResources = true
		dd.timeStarted = time.Now().Add(-6 * time.Minute)
		assert.False(t, ServerIsHealthy())
	})

	t.Run("processing but within time limit", func(t *testing.T) {
		dd.processingResources = true
		dd.timeStarted = time.Now().Add(-4 * time.Minute)
		assert.True(t, ServerIsHealthy())
	})
}
func TestClamp(t *testing.T) {
	tests := []struct {
		name     string
		min      int
		val      int
		max      int
		expected int
	}{
		{"value within range", 1, 2, 3, 2},
		{"value below min", 1, 0, 3, 1},
		{"value above max", 1, 4, 3, 3},
		{"value equals min", 1, 1, 3, 1},
		{"value equals max", 1, 3, 3, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := clamp(tt.min, tt.val, tt.max)
			assert.Equal(t, tt.expected, result)
		})
	}
}
