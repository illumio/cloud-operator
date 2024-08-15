package controller

import (
	"context"
	"testing"

	"github.com/go-logr/logr/funcr"
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
