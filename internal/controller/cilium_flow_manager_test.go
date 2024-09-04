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
