package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"k8s.io/apimachinery/pkg/watch"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

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

	t.Run("five tuple flow", func(t *testing.T) {
		flow := &pb.FiveTupleFlow{
			Layer3: &pb.IP{
				Source:      "10.0.0.1",
				Destination: "10.0.0.2",
			},
		}

		err := sm.sendNetworkFlowRequest(logger, flow)
		require.NoError(t, err)

		expected := &pb.SendKubernetesNetworkFlowsRequest{
			Request: &pb.SendKubernetesNetworkFlowsRequest_FiveTupleFlow{
				FiveTupleFlow: flow,
			},
		}
		assert.Equal(t, expected, mockStream.lastRequest)
	})
}

func TestStreamMutationObjectData(t *testing.T) {
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
		expected  *pb.KubernetesResourceMutation
	}{
		{
			name:      "added event",
			eventType: watch.Added,
			expected: &pb.KubernetesResourceMutation{
				Mutation: &pb.KubernetesResourceMutation_CreateResource{
					CreateResource: metadata,
				},
			},
		},
		{
			name:      "modified event",
			eventType: watch.Modified,
			expected: &pb.KubernetesResourceMutation{
				Mutation: &pb.KubernetesResourceMutation_UpdateResource{
					UpdateResource: metadata,
				},
			},
		},
		{
			name:      "deleted event",
			eventType: watch.Deleted,
			expected: &pb.KubernetesResourceMutation{
				Mutation: &pb.KubernetesResourceMutation_DeleteResource{
					DeleteResource: metadata,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mutation := sm.createMutationObject(metadata, tc.eventType)
			assert.Equal(t, tc.expected, mutation)
		})
	}
}

func TestSendKeepalive(t *testing.T) {
	logger := zap.NewNop()
	mockResourceStream := &mockResourceStream{}
	mockNetworkFlowsStream := &mockNetworkFlowsStream{}
	sm := &streamManager{
		streamClient: &streamClient{
			resourceStream:     mockResourceStream,
			networkFlowsStream: mockNetworkFlowsStream,
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
}
