package controller

import (
	"testing"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"k8s.io/apimachinery/pkg/watch"
)

// VersionInfo is a mock version info type for testing
type VersionInfo struct {
	Major      string
	Minor      string
	GitVersion string
}

func (v *VersionInfo) String() string {
	return v.GitVersion
}

type mockResourceStream struct {
	grpc.ClientStream
	lastRequest *pb.SendKubernetesResourcesRequest
}

func (m *mockResourceStream) Send(req *pb.SendKubernetesResourcesRequest) error {
	m.lastRequest = req
	return nil
}

func (m *mockResourceStream) Recv() (*pb.SendKubernetesResourcesResponse, error) {
	return &pb.SendKubernetesResourcesResponse{}, nil
}

func (m *mockResourceStream) CloseSend() error {
	return nil
}

type mockNetworkFlowsStream struct {
	grpc.ClientStream
	lastRequest *pb.SendKubernetesNetworkFlowsRequest
}

func (m *mockNetworkFlowsStream) Send(req *pb.SendKubernetesNetworkFlowsRequest) error {
	m.lastRequest = req
	return nil
}

func (m *mockNetworkFlowsStream) Recv() (*pb.SendKubernetesNetworkFlowsResponse, error) {
	return &pb.SendKubernetesNetworkFlowsResponse{}, nil
}

func (m *mockNetworkFlowsStream) CloseSend() error {
	return nil
}

type mockLogStream struct {
	grpc.ClientStream
	lastRequest *pb.SendLogsRequest
}

func (m *mockLogStream) Send(req *pb.SendLogsRequest) error {
	m.lastRequest = req
	return nil
}

func (m *mockLogStream) Recv() (*pb.SendLogsResponse, error) {
	return &pb.SendLogsResponse{}, nil
}

func (m *mockLogStream) CloseSend() error {
	return nil
}

type mockConfigStream struct {
	grpc.ClientStream
	lastRequest *pb.GetConfigurationUpdatesRequest
}

func (m *mockConfigStream) Send(req *pb.GetConfigurationUpdatesRequest) error {
	m.lastRequest = req
	return nil
}

func (m *mockConfigStream) Recv() (*pb.GetConfigurationUpdatesResponse, error) {
	return &pb.GetConfigurationUpdatesResponse{}, nil
}

func (m *mockConfigStream) CloseSend() error {
	return nil
}

type mockSendKubernetesResourcesClient struct {
	grpc.ClientStream
	lastRequest *pb.SendKubernetesResourcesRequest
}

func TestSendToResourceStream(t *testing.T) {
	logger := zap.NewNop()
	mockStream := &mockResourceStream{}
	sm := &streamManager{
		streamClient: &streamClient{
			resourceStream: mockStream,
		},
	}

	req := &pb.SendKubernetesResourcesRequest{
		Request: &pb.SendKubernetesResourcesRequest_ResourceData{
			ResourceData: &pb.KubernetesObjectData{
				Kind: "Pod",
				Name: "test-pod",
			},
		},
	}

	err := sm.sendToResourceStream(logger, req)
	require.NoError(t, err)
	assert.Equal(t, req, mockStream.lastRequest)
}

func TestSendObjectData(t *testing.T) {
	logger := zap.NewNop()
	mockStream := &mockResourceStream{}
	sm := &streamManager{
		streamClient: &streamClient{
			resourceStream: mockStream,
		},
	}

	metadata := &pb.KubernetesObjectData{
		Kind: "Pod",
		Name: "test-pod",
	}

	err := sm.sendObjectData(logger, metadata)
	require.NoError(t, err)

	expected := &pb.SendKubernetesResourcesRequest{
		Request: &pb.SendKubernetesResourcesRequest_ResourceData{
			ResourceData: metadata,
		},
	}
	assert.Equal(t, expected, mockStream.lastRequest)
}

func TestSendNetworkFlowRequest(t *testing.T) {
	logger := zap.NewNop()
	mockStream := &mockNetworkFlowsStream{}
	sm := &streamManager{
		streamClient: &streamClient{
			networkFlowsStream: mockStream,
		},
	}

	t.Run("cilium flow", func(t *testing.T) {
		flow := &pb.CiliumFlow{
			Layer3: &pb.IP{
				Source:      "10.0.0.1",
				Destination: "10.0.0.2",
			},
			SourceEndpoint: &pb.Endpoint{
				PodName: "pod1",
			},
			DestinationEndpoint: &pb.Endpoint{
				PodName: "pod2",
			},
		}

		err := sm.sendNetworkFlowRequest(logger, flow)
		require.NoError(t, err)

		expected := &pb.SendKubernetesNetworkFlowsRequest{
			Request: &pb.SendKubernetesNetworkFlowsRequest_CiliumFlow{
				CiliumFlow: flow,
			},
		}
		assert.Equal(t, expected, mockStream.lastRequest)
	})

	t.Run("falco flow", func(t *testing.T) {
		flow := &pb.FalcoFlow{
			Layer3: &pb.IP{
				Source:      "10.0.0.1",
				Destination: "10.0.0.2",
			},
		}

		err := sm.sendNetworkFlowRequest(logger, flow)
		require.NoError(t, err)

		expected := &pb.SendKubernetesNetworkFlowsRequest{
			Request: &pb.SendKubernetesNetworkFlowsRequest_FalcoFlow{
				FalcoFlow: flow,
			},
		}
		assert.Equal(t, expected, mockStream.lastRequest)
	})
}

func TestStreamMutationObjectData(t *testing.T) {
	logger := zap.NewNop()
	mockStream := &mockResourceStream{}
	sm := &streamManager{
		streamClient: &streamClient{
			resourceStream: mockStream,
		},
	}

	metadata := &pb.KubernetesObjectData{
		Kind: "Pod",
		Name: "test-pod",
	}

	testCases := []struct {
		name      string
		eventType watch.EventType
		expected  *pb.SendKubernetesResourcesRequest
	}{
		{
			name:      "added event",
			eventType: watch.Added,
			expected: &pb.SendKubernetesResourcesRequest{
				Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{
					KubernetesResourceMutation: &pb.KubernetesResourceMutation{
						Mutation: &pb.KubernetesResourceMutation_CreateResource{
							CreateResource: metadata,
						},
					},
				},
			},
		},
		{
			name:      "modified event",
			eventType: watch.Modified,
			expected: &pb.SendKubernetesResourcesRequest{
				Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{
					KubernetesResourceMutation: &pb.KubernetesResourceMutation{
						Mutation: &pb.KubernetesResourceMutation_UpdateResource{
							UpdateResource: metadata,
						},
					},
				},
			},
		},
		{
			name:      "deleted event",
			eventType: watch.Deleted,
			expected: &pb.SendKubernetesResourcesRequest{
				Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{
					KubernetesResourceMutation: &pb.KubernetesResourceMutation{
						Mutation: &pb.KubernetesResourceMutation_DeleteResource{
							DeleteResource: metadata,
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := sm.streamMutationObjectData(logger, metadata, tc.eventType)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, mockStream.lastRequest)
		})
	}
}

func TestSendKeepalive(t *testing.T) {
	logger := zap.NewNop()
	mockResourceStream := &mockResourceStream{}
	mockNetworkFlowsStream := &mockNetworkFlowsStream{}
	mockLogStream := &mockLogStream{}
	mockConfigStream := &mockConfigStream{}
	sm := &streamManager{
		streamClient: &streamClient{
			resourceStream:     mockResourceStream,
			networkFlowsStream: mockNetworkFlowsStream,
			logStream:          mockLogStream,
			configStream:       mockConfigStream,
		},
	}

	t.Run("resource stream", func(t *testing.T) {
		err := sm.sendKeepalive(logger, STREAM_RESOURCES)
		require.NoError(t, err)

		expected := &pb.SendKubernetesResourcesRequest{
			Request: &pb.SendKubernetesResourcesRequest_Keepalive{
				Keepalive: &pb.Keepalive{},
			},
		}
		assert.Equal(t, expected, mockResourceStream.lastRequest)
	})

	t.Run("network flows stream", func(t *testing.T) {
		err := sm.sendKeepalive(logger, STREAM_NETWORK_FLOWS)
		require.NoError(t, err)

		expected := &pb.SendKubernetesNetworkFlowsRequest{
			Request: &pb.SendKubernetesNetworkFlowsRequest_Keepalive{
				Keepalive: &pb.Keepalive{},
			},
		}
		assert.Equal(t, expected, mockNetworkFlowsStream.lastRequest)
	})

	t.Run("log stream", func(t *testing.T) {
		err := sm.sendKeepalive(logger, STREAM_LOGS)
		require.NoError(t, err)

		expected := &pb.SendLogsRequest{
			Request: &pb.SendLogsRequest_Keepalive{
				Keepalive: &pb.Keepalive{},
			},
		}
		assert.Equal(t, expected, mockLogStream.lastRequest)
	})

	t.Run("config stream", func(t *testing.T) {
		err := sm.sendKeepalive(logger, STREAM_CONFIGURATION)
		require.NoError(t, err)

		expected := &pb.GetConfigurationUpdatesRequest{
			Request: &pb.GetConfigurationUpdatesRequest_Keepalive{
				Keepalive: &pb.Keepalive{},
			},
		}
		assert.Equal(t, expected, mockConfigStream.lastRequest)
	})

	t.Run("invalid stream type", func(t *testing.T) {
		err := sm.sendKeepalive(logger, "invalid")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported stream type")
	})
}
