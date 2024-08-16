package controller

import (
	"context"
	"testing"

	"github.com/cilium/cilium/api/v1/flow"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDiscoverHubbleRelayAddress(t *testing.T) {
	tests := []struct {
		name           string
		service        *v1.Service
		expectedAddr   string
		expectedErrMsg string
	}{
		{
			name: "successful discovery",
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
		{
			name:           "service not found",
			service:        nil,
			expectedAddr:   "",
			expectedErrMsg: "services \"hubble-relay\" not found",
		},
		{
			name: "no ports in service",
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset()
			if tt.service != nil {
				clientset.CoreV1().Services("kube-system").Create(context.TODO(), tt.service, metav1.CreateOptions{})
			}

			logger := funcr.New(func(prefix, args string) {
				t.Logf("%s%s", prefix, args)
			}, funcr.Options{})

			addr, err := discoverHubbleRelayAddress(context.TODO(), logger, clientset)
			if addr != tt.expectedAddr {
				t.Errorf("expected address %s, got %s", tt.expectedAddr, addr)
			}

			if tt.expectedErrMsg != "" {
				if err == nil || err.Error() != tt.expectedErrMsg {
					t.Errorf("expected error message %s, got %v", tt.expectedErrMsg, err)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGetPortFromFlows(t *testing.T) {
	fm := &FlowManager{}

	tests := []struct {
		name         string
		l4Object     *flow.Layer4
		isSourcePort bool
		expectedPort uint32
	}{
		{
			name: "TCP destination port",
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
		{
			name: "TCP source port",
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
		{
			name: "SCTP destination port",
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
		{
			name: "SCTP source port",
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
		{
			name: "UDP destination port",
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
		{
			name: "UDP source port",
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
		{
			name:         "No L4 protocol",
			l4Object:     &flow.Layer4{},
			isSourcePort: false,
			expectedPort: 0,
		},
		{
			name:         "Nil L4",
			l4Object:     nil,
			isSourcePort: false,
			expectedPort: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port := fm.getPortFromFlows(tt.l4Object, tt.isSourcePort)
			assert.Equal(t, tt.expectedPort, port)
		})
	}
}
