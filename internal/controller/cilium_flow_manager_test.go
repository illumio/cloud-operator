package controller

import (
	"context"

	"github.com/cilium/cilium/api/v1/flow"
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8scluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func (suite *ControllerTestSuite) TestDiscoverHubbleRelayAddress() {
	ctx := context.Background()
	zapLogger := zap.New(zap.UseDevMode(true), zap.JSONEncoder())
	logger := zapLogger.WithName("test")

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
			expectedErrMsg: "services \"hubble-relay\" not found",
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
			expectedErrMsg: "hubble relay service has no ports",
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			clientset := fake.NewSimpleClientset()
			if tt.service != nil {
				_, err := clientset.CoreV1().Services("kube-system").Create(context.TODO(), tt.service, metav1.CreateOptions{})
				assert.NoError(suite.T(), err)
			}

			addr, err := discoverHubbleRelayAddress(ctx, logger, clientset)
			assert.Equal(suite.T(), tt.expectedAddr, addr)

			if tt.expectedErrMsg != "" {
				assert.EqualError(suite.T(), err, tt.expectedErrMsg)
			} else {
				assert.NoError(suite.T(), err)
			}
		})
	}
}

func (suite *ControllerTestSuite) TestGetPortFromFlows() {
	fm := &FlowManager{}

	tests := map[string]struct {
		l4Object     *flow.Layer4
		isSourcePort bool
		expectedPort uint32
	}{
		"TCP destination port": {
			l4Object: &flow.Layer4{
				Protocol: &flow.Layer4_TCP{
					TCP: &flow.TCP{
						DestinationPort: 80,
						SourcePort:      12345,
					},
				},
			},
			isSourcePort: false,
			expectedPort: 80,
		},
		"TCP source port": {
			l4Object: &flow.Layer4{
				Protocol: &flow.Layer4_TCP{
					TCP: &flow.TCP{
						DestinationPort: 80,
						SourcePort:      12345,
					},
				},
			},
			isSourcePort: true,
			expectedPort: 12345,
		},
		"SCTP destination port": {
			l4Object: &flow.Layer4{
				Protocol: &flow.Layer4_SCTP{
					SCTP: &flow.SCTP{
						DestinationPort: 5678,
						SourcePort:      6789,
					},
				},
			},
			isSourcePort: false,
			expectedPort: 5678,
		},
		"SCTP source port": {
			l4Object: &flow.Layer4{
				Protocol: &flow.Layer4_SCTP{
					SCTP: &flow.SCTP{
						DestinationPort: 5678,
						SourcePort:      6789,
					},
				},
			},
			isSourcePort: true,
			expectedPort: 6789,
		},
		"UDP destination port": {
			l4Object: &flow.Layer4{
				Protocol: &flow.Layer4_UDP{
					UDP: &flow.UDP{
						DestinationPort: 53,
						SourcePort:      54321,
					},
				},
			},
			isSourcePort: false,
			expectedPort: 53,
		},
		"UDP source port": {
			l4Object: &flow.Layer4{
				Protocol: &flow.Layer4_UDP{
					UDP: &flow.UDP{
						DestinationPort: 53,
						SourcePort:      54321,
					},
				},
			},
			isSourcePort: true,
			expectedPort: 54321,
		},
		"No L4 protocol": {
			l4Object:     &flow.Layer4{},
			isSourcePort: false,
			expectedPort: 0,
		},
		"Nil L4": {
			l4Object:     nil,
			isSourcePort: false,
			expectedPort: 0,
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			port := fm.getPortFromFlows(tt.l4Object, tt.isSourcePort)
			assert.Equal(suite.T(), tt.expectedPort, port)
		})
	}
}

func (suite *ControllerTestSuite) TestConvertToProtoWorkloads() {
	tests := map[string]struct {
		input    []*flow.Workload
		expected []*pb.Workload
	}{
		"empty input": {
			input:    []*flow.Workload{},
			expected: []*pb.Workload{},
		},
		"single workload": {
			input: []*flow.Workload{
				{
					Name: "workload1",
					Kind: "id1",
				},
			},
			expected: []*pb.Workload{
				{
					Name: "workload1",
					Kind: "id1",
				},
			},
		},
		"multiple workloads": {
			input: []*flow.Workload{
				{
					Name: "workload1",
					Kind: "id1",
				},
				{
					Name: "workload2",
					Kind: "id2",
				},
			},
			expected: []*pb.Workload{
				{
					Name: "workload1",
					Kind: "id1",
				},
				{
					Name: "workload2",
					Kind: "id2",
				},
			},
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			result := convertToProtoWorkloads(tt.input)
			assert.Equal(suite.T(), tt.expected, result)
		})
	}
}
