// Copyright 2024 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/cilium/cilium/api/v1/flow"
	observer "github.com/cilium/cilium/api/v1/observer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

//nolint:maintidx // table-driven test with comprehensive test cases
func TestConvertCiliumFlow(t *testing.T) {
	tests := map[string]struct {
		input    *observer.GetFlowsResponse
		expected *pb.CiliumFlow
	}{
		"nil time": {
			input: &observer.GetFlowsResponse{
				ResponseTypes: &observer.GetFlowsResponse_Flow{
					Flow: &flow.Flow{
						Time:             nil,
						TrafficDirection: flow.TrafficDirection_INGRESS,
						Verdict:          flow.Verdict_FORWARDED,
						IP:               &flow.IP{Source: "1.1.1.1", Destination: "2.2.2.2"},
						L4:               &flow.Layer4{Protocol: &flow.Layer4_TCP{TCP: &flow.TCP{SourcePort: 80}}},
					},
				},
			},
			expected: nil,
		},
		"unknown traffic direction still processed": {
			input: &observer.GetFlowsResponse{
				ResponseTypes: &observer.GetFlowsResponse_Flow{
					Flow: &flow.Flow{
						Time:               &timestamppb.Timestamp{Seconds: 1234567890},
						TrafficDirection:   flow.TrafficDirection_TRAFFIC_DIRECTION_UNKNOWN,
						Verdict:            flow.Verdict_FORWARDED,
						IP:                 &flow.IP{Source: "1.1.1.1", Destination: "2.2.2.2"},
						L4:                 &flow.Layer4{Protocol: &flow.Layer4_TCP{TCP: &flow.TCP{SourcePort: 80}}},
						DestinationService: &flow.Service{},
					},
				},
			},
			expected: &pb.CiliumFlow{
				Time:               &timestamppb.Timestamp{Seconds: 1234567890},
				TrafficDirection:   pb.TrafficDirection_TRAFFIC_DIRECTION_TRAFFIC_DIRECTION_UNKNOWN_UNSPECIFIED,
				Verdict:            pb.Verdict_VERDICT_FORWARDED,
				Layer3:             &pb.IP{Source: "1.1.1.1", Destination: "2.2.2.2"},
				Layer4:             &pb.Layer4{Protocol: &pb.Layer4_Tcp{Tcp: &pb.TCP{SourcePort: 80}}},
				DestinationService: &pb.Service{},
				EgressAllowedBy:    []*pb.Policy{},
				IngressAllowedBy:   []*pb.Policy{},
				EgressDeniedBy:     []*pb.Policy{},
				IngressDeniedBy:    []*pb.Policy{},
			},
		},
		"nil IP": {
			input: &observer.GetFlowsResponse{
				ResponseTypes: &observer.GetFlowsResponse_Flow{
					Flow: &flow.Flow{
						Time:             &timestamppb.Timestamp{Seconds: 1234567890},
						TrafficDirection: flow.TrafficDirection_INGRESS,
						Verdict:          flow.Verdict_FORWARDED,
						IP:               nil,
						L4:               &flow.Layer4{Protocol: &flow.Layer4_TCP{TCP: &flow.TCP{SourcePort: 80}}},
					},
				},
			},
			expected: nil,
		},
		"nil L4": {
			input: &observer.GetFlowsResponse{
				ResponseTypes: &observer.GetFlowsResponse_Flow{
					Flow: &flow.Flow{
						Time:             &timestamppb.Timestamp{Seconds: 1234567890},
						TrafficDirection: flow.TrafficDirection_INGRESS,
						Verdict:          flow.Verdict_FORWARDED,
						IP:               &flow.IP{Source: "1.1.1.1", Destination: "2.2.2.2"},
						L4:               nil,
					},
				},
			},
			expected: nil,
		},
		"valid TCP flow": {
			input: &observer.GetFlowsResponse{
				ResponseTypes: &observer.GetFlowsResponse_Flow{
					Flow: &flow.Flow{
						Time:             &timestamppb.Timestamp{Seconds: 1234567890},
						NodeName:         "node1",
						TrafficDirection: flow.TrafficDirection_INGRESS,
						Verdict:          flow.Verdict_FORWARDED,
						IP: &flow.IP{
							Source:      "192.168.1.1",
							Destination: "192.168.1.2",
							IpVersion:   flow.IPVersion_IPv4,
						},
						L4: &flow.Layer4{
							Protocol: &flow.Layer4_TCP{
								TCP: &flow.TCP{
									SourcePort:      1234,
									DestinationPort: 5678,
								},
							},
						},
						DestinationService: &flow.Service{Name: "svc1", Namespace: "ns1"},
						IsReply:            wrapperspb.Bool(false),
					},
				},
			},
			expected: &pb.CiliumFlow{
				Time:             &timestamppb.Timestamp{Seconds: 1234567890},
				NodeName:         "node1",
				TrafficDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_INGRESS,
				Verdict:          pb.Verdict_VERDICT_FORWARDED,
				Layer3: &pb.IP{
					Source:      "192.168.1.1",
					Destination: "192.168.1.2",
					IpVersion:   pb.IPVersion_IP_VERSION_IPV4,
				},
				Layer4: &pb.Layer4{
					Protocol: &pb.Layer4_Tcp{
						Tcp: &pb.TCP{
							SourcePort:      1234,
							DestinationPort: 5678,
						},
					},
				},
				DestinationService: &pb.Service{Name: "svc1", Namespace: "ns1"},
				EgressAllowedBy:    []*pb.Policy{},
				IngressAllowedBy:   []*pb.Policy{},
				EgressDeniedBy:     []*pb.Policy{},
				IngressDeniedBy:    []*pb.Policy{},
				IsReply:            wrapperspb.Bool(false),
			},
		},
		"valid UDP flow with endpoints": {
			input: &observer.GetFlowsResponse{
				ResponseTypes: &observer.GetFlowsResponse_Flow{
					Flow: &flow.Flow{
						Time:             &timestamppb.Timestamp{Seconds: 1234567890},
						NodeName:         "node2",
						TrafficDirection: flow.TrafficDirection_EGRESS,
						Verdict:          flow.Verdict_DROPPED,
						IP: &flow.IP{
							Source:      "10.0.0.1",
							Destination: "10.0.0.2",
							IpVersion:   flow.IPVersion_IPv4,
						},
						L4: &flow.Layer4{
							Protocol: &flow.Layer4_UDP{
								UDP: &flow.UDP{
									SourcePort:      5000,
									DestinationPort: 53,
								},
							},
						}, Source: &flow.Endpoint{
							ID:          1,
							ClusterName: "cluster1",
							Namespace:   "ns1",
							Labels:      []string{"app=test"},
							PodName:     "pod1",
							Workloads: []*flow.Workload{
								{Name: "workload1", Kind: "Deployment"},
							},
						},
						Destination: &flow.Endpoint{
							ID:          2,
							ClusterName: "cluster1",
							Namespace:   "kube-system",
							Labels:      []string{"k8s-app=kube-dns"},
							PodName:     "coredns-abc123",
						},
						DestinationService: &flow.Service{},
						IsReply:            wrapperspb.Bool(true),
					},
				},
			},
			expected: &pb.CiliumFlow{
				Time:             &timestamppb.Timestamp{Seconds: 1234567890},
				NodeName:         "node2",
				TrafficDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS,
				Verdict:          pb.Verdict_VERDICT_DROPPED,
				Layer3: &pb.IP{
					Source:      "10.0.0.1",
					Destination: "10.0.0.2",
					IpVersion:   pb.IPVersion_IP_VERSION_IPV4,
				},
				Layer4: &pb.Layer4{
					Protocol: &pb.Layer4_Udp{
						Udp: &pb.UDP{
							SourcePort:      5000,
							DestinationPort: 53,
						},
					},
				},
				SourceEndpoint: &pb.Endpoint{
					Uid:         1,
					ClusterName: "cluster1",
					Namespace:   "ns1",
					Labels:      []string{"app=test"},
					PodName:     "pod1",
					Workloads:   []*pb.Workload{{Name: "workload1", Kind: "Deployment"}},
				},
				DestinationEndpoint: &pb.Endpoint{
					Uid:         2,
					ClusterName: "cluster1",
					Namespace:   "kube-system",
					Labels:      []string{"k8s-app=kube-dns"},
					PodName:     "coredns-abc123",
					Workloads:   []*pb.Workload{},
				},
				DestinationService: &pb.Service{},
				EgressAllowedBy:    []*pb.Policy{},
				IngressAllowedBy:   []*pb.Policy{},
				EgressDeniedBy:     []*pb.Policy{},
				IngressDeniedBy:    []*pb.Policy{},
				IsReply:            wrapperspb.Bool(true),
			},
		},
		"flow with policies": {
			input: &observer.GetFlowsResponse{
				ResponseTypes: &observer.GetFlowsResponse_Flow{
					Flow: &flow.Flow{
						Time:             &timestamppb.Timestamp{Seconds: 1234567890},
						TrafficDirection: flow.TrafficDirection_INGRESS,
						Verdict:          flow.Verdict_AUDIT,
						IP: &flow.IP{
							Source:      "1.1.1.1",
							Destination: "2.2.2.2",
						},
						L4: &flow.Layer4{
							Protocol: &flow.Layer4_TCP{
								TCP: &flow.TCP{SourcePort: 80, DestinationPort: 8080},
							},
						},
						DestinationService: &flow.Service{},
						IngressAllowedBy: []*flow.Policy{
							{Name: "allow-all", Namespace: "default", Kind: "NetworkPolicy", Revision: 1},
						},
						EgressDeniedBy: []*flow.Policy{
							{Name: "deny-egress", Namespace: "secure", Kind: "CiliumNetworkPolicy", Revision: 2, Labels: []string{"team=security"}},
						},
					},
				},
			},
			expected: &pb.CiliumFlow{
				Time:             &timestamppb.Timestamp{Seconds: 1234567890},
				TrafficDirection: pb.TrafficDirection_TRAFFIC_DIRECTION_INGRESS,
				Verdict:          pb.Verdict_VERDICT_AUDIT,
				Layer3: &pb.IP{
					Source:      "1.1.1.1",
					Destination: "2.2.2.2",
				},
				Layer4: &pb.Layer4{
					Protocol: &pb.Layer4_Tcp{
						Tcp: &pb.TCP{SourcePort: 80, DestinationPort: 8080},
					},
				},
				DestinationService: &pb.Service{},
				EgressAllowedBy:    []*pb.Policy{},
				IngressAllowedBy:   []*pb.Policy{{Name: "allow-all", Namespace: "default", Kind: "NetworkPolicy", Revision: 1}},
				EgressDeniedBy:     []*pb.Policy{{Name: "deny-egress", Namespace: "secure", Kind: "CiliumNetworkPolicy", Revision: 2, Labels: []string{"team=security"}}},
				IngressDeniedBy:    []*pb.Policy{},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := ConvertCiliumFlow(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertCiliumLayer4_AllProtocols(t *testing.T) {
	tests := map[string]struct {
		input    *flow.Layer4
		expected *pb.Layer4
	}{
		"tcp protocol": {
			input: &flow.Layer4{
				Protocol: &flow.Layer4_TCP{
					TCP: &flow.TCP{SourcePort: 1234, DestinationPort: 5678},
				},
			},
			expected: &pb.Layer4{
				Protocol: &pb.Layer4_Tcp{
					Tcp: &pb.TCP{SourcePort: 1234, DestinationPort: 5678},
				},
			},
		},
		"udp protocol": {
			input: &flow.Layer4{
				Protocol: &flow.Layer4_UDP{
					UDP: &flow.UDP{SourcePort: 1234, DestinationPort: 5678},
				},
			},
			expected: &pb.Layer4{
				Protocol: &pb.Layer4_Udp{
					Udp: &pb.UDP{SourcePort: 1234, DestinationPort: 5678},
				},
			},
		},
		"icmpv4 protocol": {
			input: &flow.Layer4{
				Protocol: &flow.Layer4_ICMPv4{
					ICMPv4: &flow.ICMPv4{Type: 8, Code: 0},
				},
			},
			expected: &pb.Layer4{
				Protocol: &pb.Layer4_Icmpv4{
					Icmpv4: &pb.ICMPv4{Type: 8, Code: 0},
				},
			},
		},
		"icmpv6 protocol": {
			input: &flow.Layer4{
				Protocol: &flow.Layer4_ICMPv6{
					ICMPv6: &flow.ICMPv6{Type: 128, Code: 0},
				},
			},
			expected: &pb.Layer4{
				Protocol: &pb.Layer4_Icmpv6{
					Icmpv6: &pb.ICMPv6{Type: 128, Code: 0},
				},
			},
		},
		"sctp protocol": {
			input: &flow.Layer4{
				Protocol: &flow.Layer4_SCTP{
					SCTP: &flow.SCTP{SourcePort: 1234, DestinationPort: 5678},
				},
			},
			expected: &pb.Layer4{
				Protocol: &pb.Layer4_Sctp{
					Sctp: &pb.SCTP{SourcePort: 1234, DestinationPort: 5678},
				},
			},
		},
		"nil input": {
			input:    nil,
			expected: nil,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := convertCiliumLayer4(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertCiliumIP(t *testing.T) {
	tests := map[string]struct {
		input    *flow.IP
		expected *pb.IP
	}{
		"nil input": {
			input:    nil,
			expected: nil,
		},
		"IPv4": {
			input: &flow.IP{
				Source:      "192.168.1.1",
				Destination: "192.168.1.2",
				IpVersion:   flow.IPVersion_IPv4,
			},
			expected: &pb.IP{
				Source:      "192.168.1.1",
				Destination: "192.168.1.2",
				IpVersion:   pb.IPVersion_IP_VERSION_IPV4,
			},
		},
		"IPv6": {
			input: &flow.IP{
				Source:      "2001:db8::1",
				Destination: "2001:db8::2",
				IpVersion:   flow.IPVersion_IPv6,
			},
			expected: &pb.IP{
				Source:      "2001:db8::1",
				Destination: "2001:db8::2",
				IpVersion:   pb.IPVersion_IP_VERSION_IPV6,
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := convertCiliumIP(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertCiliumWorkflows(t *testing.T) {
	tests := map[string]struct {
		input    []*flow.Workload
		expected []*pb.Workload
	}{
		"single workload": {
			input:    []*flow.Workload{{Name: "workload1", Kind: "Deployment"}},
			expected: []*pb.Workload{{Name: "workload1", Kind: "Deployment"}},
		},
		"multiple workloads": {
			input: []*flow.Workload{
				{Name: "workload1", Kind: "Deployment"},
				{Name: "workload2", Kind: "StatefulSet"},
			},
			expected: []*pb.Workload{
				{Name: "workload1", Kind: "Deployment"},
				{Name: "workload2", Kind: "StatefulSet"},
			},
		},
		"empty workloads": {
			input:    []*flow.Workload{},
			expected: []*pb.Workload{},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := convertCiliumWorkflows(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertCiliumPolicies(t *testing.T) {
	tests := map[string]struct {
		input    []*flow.Policy
		expected []*pb.Policy
	}{
		"single policy": {
			input: []*flow.Policy{
				{Name: "policy1", Namespace: "ns1", Kind: "NetworkPolicy", Revision: 1, Labels: []string{"app=test"}},
			},
			expected: []*pb.Policy{
				{Name: "policy1", Namespace: "ns1", Kind: "NetworkPolicy", Revision: 1, Labels: []string{"app=test"}},
			},
		},
		"multiple policies": {
			input: []*flow.Policy{
				{Name: "policy1", Namespace: "ns1", Kind: "NetworkPolicy", Revision: 1},
				{Name: "policy2", Namespace: "ns2", Kind: "CiliumNetworkPolicy", Revision: 2},
			},
			expected: []*pb.Policy{
				{Name: "policy1", Namespace: "ns1", Kind: "NetworkPolicy", Revision: 1},
				{Name: "policy2", Namespace: "ns2", Kind: "CiliumNetworkPolicy", Revision: 2},
			},
		},
		"empty policies": {
			input:    []*flow.Policy{},
			expected: []*pb.Policy{},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := convertCiliumPolicies(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// mockObserverClient mocks the observer.ObserverClient interface.
type mockObserverClient struct {
	mock.Mock
}

func (m *mockObserverClient) GetFlows(ctx context.Context, in *observer.GetFlowsRequest, opts ...grpc.CallOption) (observer.Observer_GetFlowsClient, error) {
	args := m.Called(ctx, in, opts)
	if client, ok := args.Get(0).(observer.Observer_GetFlowsClient); ok {
		return client, args.Error(1)
	}

	return nil, args.Error(1)
}

func (m *mockObserverClient) GetAgentEvents(_ context.Context, _ *observer.GetAgentEventsRequest, _ ...grpc.CallOption) (observer.Observer_GetAgentEventsClient, error) {
	return nil, nil //nolint:nilnil // stub method not used in tests
}

func (m *mockObserverClient) GetDebugEvents(_ context.Context, _ *observer.GetDebugEventsRequest, _ ...grpc.CallOption) (observer.Observer_GetDebugEventsClient, error) {
	return nil, nil //nolint:nilnil // stub method not used in tests
}

func (m *mockObserverClient) GetNodes(_ context.Context, _ *observer.GetNodesRequest, _ ...grpc.CallOption) (*observer.GetNodesResponse, error) {
	return nil, nil //nolint:nilnil // stub method not used in tests
}

func (m *mockObserverClient) GetNamespaces(_ context.Context, _ *observer.GetNamespacesRequest, _ ...grpc.CallOption) (*observer.GetNamespacesResponse, error) {
	return nil, nil //nolint:nilnil // stub method not used in tests
}

func (m *mockObserverClient) ServerStatus(_ context.Context, _ *observer.ServerStatusRequest, _ ...grpc.CallOption) (*observer.ServerStatusResponse, error) {
	return nil, nil //nolint:nilnil // stub method not used in tests
}

// mockGetFlowsClient mocks the observer.Observer_GetFlowsClient stream.
type mockGetFlowsClient struct {
	mock.Mock
	grpc.ClientStream
}

func (m *mockGetFlowsClient) Recv() (*observer.GetFlowsResponse, error) {
	args := m.Called()
	if resp, ok := args.Get(0).(*observer.GetFlowsResponse); ok {
		return resp, args.Error(1)
	}

	return nil, args.Error(1)
}

func (m *mockGetFlowsClient) Header() (metadata.MD, error) {
	return nil, nil //nolint:nilnil // stub method not used in tests
}

func (m *mockGetFlowsClient) Trailer() metadata.MD {
	return nil
}

func (m *mockGetFlowsClient) CloseSend() error {
	args := m.Called()

	return args.Error(0)
}

func (m *mockGetFlowsClient) Context() context.Context {
	return context.Background()
}

func (m *mockGetFlowsClient) SendMsg(msg any) error {
	return nil
}

func (m *mockGetFlowsClient) RecvMsg(msg any) error {
	return nil
}

// mockFlowSink mocks the FlowSink interface.
type mockCiliumFlowSink struct {
	mock.Mock
}

func (m *mockCiliumFlowSink) CacheFlow(ctx context.Context, flow pb.Flow) error {
	args := m.Called(ctx, flow)

	return args.Error(0)
}

func (m *mockCiliumFlowSink) IncrementFlowsReceived() {
	m.Called()
}

func TestExportCiliumFlows_Success(t *testing.T) {
	logger := zap.NewNop()
	mockClient := &mockObserverClient{}
	mockStream := &mockGetFlowsClient{}
	mockSink := &mockCiliumFlowSink{}

	collector := &CiliumFlowCollector{
		logger: logger,
		client: mockClient,
	}

	// Create a valid flow response
	flowResp := &observer.GetFlowsResponse{
		ResponseTypes: &observer.GetFlowsResponse_Flow{
			Flow: &flow.Flow{
				Time:               &timestamppb.Timestamp{Seconds: 1234567890},
				TrafficDirection:   flow.TrafficDirection_INGRESS,
				Verdict:            flow.Verdict_FORWARDED,
				IP:                 &flow.IP{Source: "1.1.1.1", Destination: "2.2.2.2"},
				L4:                 &flow.Layer4{Protocol: &flow.Layer4_TCP{TCP: &flow.TCP{SourcePort: 80}}},
				DestinationService: &flow.Service{},
			},
		},
	}

	mockClient.On("GetFlows", mock.Anything, mock.Anything, mock.Anything).Return(mockStream, nil).Once()
	mockStream.On("Recv").Return(flowResp, nil).Once()
	mockStream.On("Recv").Return(nil, io.EOF).Once()
	mockStream.On("CloseSend").Return(nil).Once()
	mockSink.On("CacheFlow", mock.Anything, mock.Anything).Return(nil).Once()
	mockSink.On("IncrementFlowsReceived").Once()

	err := collector.ExportCiliumFlows(context.Background(), mockSink)

	require.Error(t, err) // io.EOF is expected
	assert.Equal(t, io.EOF, err)
	mockClient.AssertExpectations(t)
	mockStream.AssertExpectations(t)
	mockSink.AssertExpectations(t)
}

func TestExportCiliumFlows_GetFlowsError(t *testing.T) {
	logger := zap.NewNop()
	mockClient := &mockObserverClient{}
	mockSink := &mockCiliumFlowSink{}

	collector := &CiliumFlowCollector{
		logger: logger,
		client: mockClient,
	}

	expectedErr := errors.New("connection failed")
	mockClient.On("GetFlows", mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedErr).Once()

	err := collector.ExportCiliumFlows(context.Background(), mockSink)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection failed")
	mockClient.AssertExpectations(t)
}

func TestExportCiliumFlows_ContextCanceled(t *testing.T) {
	logger := zap.NewNop()
	mockClient := &mockObserverClient{}
	mockStream := &mockGetFlowsClient{}
	mockSink := &mockCiliumFlowSink{}

	collector := &CiliumFlowCollector{
		logger: logger,
		client: mockClient,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockClient.On("GetFlows", mock.Anything, mock.Anything, mock.Anything).Return(mockStream, nil).Once()
	mockStream.On("CloseSend").Return(nil).Once()

	err := collector.ExportCiliumFlows(ctx, mockSink)

	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	mockClient.AssertExpectations(t)
	mockStream.AssertExpectations(t)
}

func TestExportCiliumFlows_CacheFlowError(t *testing.T) {
	logger := zap.NewNop()
	mockClient := &mockObserverClient{}
	mockStream := &mockGetFlowsClient{}
	mockSink := &mockCiliumFlowSink{}

	collector := &CiliumFlowCollector{
		logger: logger,
		client: mockClient,
	}

	flowResp := &observer.GetFlowsResponse{
		ResponseTypes: &observer.GetFlowsResponse_Flow{
			Flow: &flow.Flow{
				Time:               &timestamppb.Timestamp{Seconds: 1234567890},
				TrafficDirection:   flow.TrafficDirection_INGRESS,
				Verdict:            flow.Verdict_FORWARDED,
				IP:                 &flow.IP{Source: "1.1.1.1", Destination: "2.2.2.2"},
				L4:                 &flow.Layer4{Protocol: &flow.Layer4_TCP{TCP: &flow.TCP{SourcePort: 80}}},
				DestinationService: &flow.Service{},
			},
		},
	}

	cacheErr := errors.New("cache full")

	mockClient.On("GetFlows", mock.Anything, mock.Anything, mock.Anything).Return(mockStream, nil).Once()
	mockStream.On("Recv").Return(flowResp, nil).Once()
	mockStream.On("CloseSend").Return(nil).Once()
	mockSink.On("CacheFlow", mock.Anything, mock.Anything).Return(cacheErr).Once()

	err := collector.ExportCiliumFlows(context.Background(), mockSink)

	require.Error(t, err)
	assert.Equal(t, cacheErr, err)
	mockClient.AssertExpectations(t)
	mockStream.AssertExpectations(t)
	mockSink.AssertExpectations(t)
}

func TestExportCiliumFlows_SkipsNilConvertedFlow(t *testing.T) {
	logger := zap.NewNop()
	mockClient := &mockObserverClient{}
	mockStream := &mockGetFlowsClient{}
	mockSink := &mockCiliumFlowSink{}

	collector := &CiliumFlowCollector{
		logger: logger,
		client: mockClient,
	}

	// Flow with nil Time will be converted to nil
	invalidFlowResp := &observer.GetFlowsResponse{
		ResponseTypes: &observer.GetFlowsResponse_Flow{
			Flow: &flow.Flow{
				Time: nil, // Will cause ConvertCiliumFlow to return nil
			},
		},
	}

	mockClient.On("GetFlows", mock.Anything, mock.Anything, mock.Anything).Return(mockStream, nil).Once()
	mockStream.On("Recv").Return(invalidFlowResp, nil).Once()
	mockStream.On("Recv").Return(nil, io.EOF).Once()
	mockStream.On("CloseSend").Return(nil).Once()
	// CacheFlow should NOT be called since the flow is nil

	err := collector.ExportCiliumFlows(context.Background(), mockSink)

	require.Error(t, err)
	assert.Equal(t, io.EOF, err)
	mockClient.AssertExpectations(t)
	mockStream.AssertExpectations(t)
	// mockSink.CacheFlow should not have been called
	mockSink.AssertNotCalled(t, "CacheFlow")
}
