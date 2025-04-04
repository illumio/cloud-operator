// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"

	"github.com/cilium/cilium/api/v1/flow"
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func (suite *ControllerTestSuite) TestDiscoverHubbleRelayAddress() {
	ctx := context.Background()

	tests := map[string]struct {
		service        *v1.Service
		expectedAddr   string
		expectedErrMsg string
	}{
		"successful discovery": {
			service: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hubble-relay",
					Namespace: "kube-system",
				},
				Spec: v1.ServiceSpec{
					ClusterIP: "10.0.0.1",
					Ports: []v1.ServicePort{
						{
							Port: 80,
						},
					},
				},
			},
			expectedAddr:   "10.0.0.1:80",
			expectedErrMsg: "",
		},
		"service not found": {
			service:        nil,
			expectedAddr:   "",
			expectedErrMsg: "hubble Relay service not found; disabling Cilium flow collection",
		},
		"no ports in service": {
			service: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hubble-relay",
					Namespace: "kube-system",
				},
				Spec: v1.ServiceSpec{
					ClusterIP: "10.0.0.1",
					Ports:     []v1.ServicePort{},
				},
			},
			expectedAddr:   "",
			expectedErrMsg: "hubble Relay service has no ports; disabling Cilium flow collection",
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			clientset := fake.NewSimpleClientset()
			if tt.service != nil {
				_, err := clientset.CoreV1().Services("kube-system").Create(context.TODO(), tt.service, metav1.CreateOptions{})
				assert.NoError(suite.T(), err)
			}

			addr, err := discoverCiliumHubbleRelayAddress(ctx, "kube-system", clientset)
			assert.Equal(suite.T(), tt.expectedAddr, addr)

			if tt.expectedErrMsg != "" {
				assert.EqualError(suite.T(), err, tt.expectedErrMsg)
			} else {
				assert.NoError(suite.T(), err)
			}
		})
	}
}

func (suite *ControllerTestSuite) TestConvertLayer4() {
	tests := map[string]struct {
		input    *flow.Layer4
		expected *pb.Layer4
	}{
		"tcp protocol": {
			input: &flow.Layer4{
				Protocol: &flow.Layer4_TCP{
					TCP: &flow.TCP{
						SourcePort:      1234,
						DestinationPort: 5678,
					},
				},
			},
			expected: &pb.Layer4{
				Protocol: &pb.Layer4_Tcp{
					Tcp: &pb.TCP{
						SourcePort:      1234,
						DestinationPort: 5678,
					},
				},
			},
		},
		"udp protocol": {
			input: &flow.Layer4{
				Protocol: &flow.Layer4_UDP{
					UDP: &flow.UDP{
						SourcePort:      1234,
						DestinationPort: 5678,
					},
				},
			},
			expected: &pb.Layer4{
				Protocol: &pb.Layer4_Udp{
					Udp: &pb.UDP{
						SourcePort:      1234,
						DestinationPort: 5678,
					},
				},
			},
		},
		"icmpv4 protocol": {
			input: &flow.Layer4{
				Protocol: &flow.Layer4_ICMPv4{
					ICMPv4: &flow.ICMPv4{
						Type: 8,
						Code: 0,
					},
				},
			},
			expected: &pb.Layer4{
				Protocol: &pb.Layer4_Icmpv4{
					Icmpv4: &pb.ICMPv4{
						Type: 8,
						Code: 0,
					},
				},
			},
		},
		"icmpv6 protocol": {
			input: &flow.Layer4{
				Protocol: &flow.Layer4_ICMPv6{
					ICMPv6: &flow.ICMPv6{
						Type: 128,
						Code: 0,
					},
				},
			},
			expected: &pb.Layer4{
				Protocol: &pb.Layer4_Icmpv6{
					Icmpv6: &pb.ICMPv6{
						Type: 128,
						Code: 0,
					},
				},
			},
		},
		"sctp protocol": {
			input: &flow.Layer4{
				Protocol: &flow.Layer4_SCTP{
					SCTP: &flow.SCTP{
						SourcePort:      1234,
						DestinationPort: 5678,
					},
				},
			},
			expected: &pb.Layer4{
				Protocol: &pb.Layer4_Sctp{
					Sctp: &pb.SCTP{
						SourcePort:      1234,
						DestinationPort: 5678,
					},
				},
			},
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			got := convertCiliumLayer4(tt.input)
			assert.Equal(suite.T(), tt.expected, got, "Expected: %v, got: %v", tt.expected, got)
		})
	}
}

func (suite *ControllerTestSuite) TestConvertCiliumWorkflows() {
	tests := map[string]struct {
		input    []*flow.Workload
		expected []*pb.Workload
	}{
		"single workload": {
			input: []*flow.Workload{
				{
					Name: "workload1",
					Kind: "kind1",
				},
			},
			expected: []*pb.Workload{
				{
					Name: "workload1",
					Kind: "kind1",
				},
			},
		},
		"multiple workloads": {
			input: []*flow.Workload{
				{
					Name: "workload1",
					Kind: "kind1",
				},
				{
					Name: "workload2",
					Kind: "kind2",
				},
			},
			expected: []*pb.Workload{
				{
					Name: "workload1",
					Kind: "kind1",
				},
				{
					Name: "workload2",
					Kind: "kind2",
				},
			},
		},
		"no workloads": {
			input:    []*flow.Workload{},
			expected: []*pb.Workload{},
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			got := convertCiliumWorkflows(tt.input)
			assert.Equal(suite.T(), tt.expected, got, "Expected: %v, got: %v", tt.expected, got)
		})
	}
}

func (suite *ControllerTestSuite) TestConvertCiliumPolicies() {
	tests := map[string]struct {
		input    []*flow.Policy
		expected []*pb.Policy
	}{
		"single policy": {
			input: []*flow.Policy{
				{
					Name:      "policy1",
					Namespace: "namespace1",
					Labels:    []string{"val1", "val2"},
					Revision:  1,
				},
			},
			expected: []*pb.Policy{
				{
					Name:      "policy1",
					Namespace: "namespace1",
					Labels:    []string{"val1", "val2"},
					Revision:  1,
				},
			},
		},
		"multiple policies": {
			input: []*flow.Policy{
				{
					Name:      "policy1",
					Namespace: "namespace1",
					Labels:    []string{"val1", "val2"},
					Revision:  1,
				},
				{
					Name:      "policy2",
					Namespace: "namespace2",
					Labels:    []string{"val1", "val2"},
					Revision:  2,
				},
			},
			expected: []*pb.Policy{
				{
					Name:      "policy1",
					Namespace: "namespace1",
					Labels:    []string{"val1", "val2"},
					Revision:  1,
				},
				{
					Name:      "policy2",
					Namespace: "namespace2",
					Labels:    []string{"val1", "val2"},
					Revision:  2,
				},
			},
		},
		"no policies": {
			input:    []*flow.Policy{},
			expected: []*pb.Policy{},
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			got := convertCiliumPolicies(tt.input)
			assert.Equal(suite.T(), tt.expected, got, "Expected: %v, got: %v", tt.expected, got)
		})
	}
}

func (suite *ControllerTestSuite) TestConvertCiliumIP() {
	tests := map[string]struct {
		input    *flow.IP
		expected *pb.IP
	}{
		"nil input": {
			input:    nil,
			expected: nil,
		},
		"valid input": {
			input: &flow.IP{
				Source:      "192.168.1.1",
				Destination: "192.168.1.2",
				IpVersion:   flow.IPVersion_IPv4,
			},
			expected: &pb.IP{
				Source:      "192.168.1.1",
				Destination: "192.168.1.2",
				IpVersion:   pb.IPVersion(flow.IPVersion_IPv4),
			},
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			result := convertCiliumIP(tt.input)
			assert.Equal(suite.T(), tt.expected, result)
		})
	}
}
