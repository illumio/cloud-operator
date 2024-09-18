// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// Mocked function to replace the real GetClusterID function for testing
func GetClusterIDWithClient(ctx context.Context, logger *zap.SugaredLogger, clientset *fake.Clientset) (string, error) {
	namespace, err := clientset.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
	if err != nil {
		logger.Errorw("Failed to get kube-system namespace", "error", err)
		return "", err
	}
	return string(namespace.UID), nil
}

func (suite *ControllerTestSuite) TestGetClusterID() {
	ctx := context.Background()
	logger := newCustomLogger(suite.T())

	tests := map[string]struct {
		setup     func() *fake.Clientset
		want      string
		expectErr bool
	}{
		"success": {
			setup: func() *fake.Clientset {
				client := fake.NewSimpleClientset(&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "kube-system",
						UID:  "test-uid",
					},
				})
				return client
			},
			want:      "test-uid",
			expectErr: false,
		},
		"namespace-not-found": {
			setup: func() *fake.Clientset {
				client := fake.NewSimpleClientset()
				return client
			},
			want:      "",
			expectErr: true,
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			client := tt.setup()
			got, err := GetClusterIDWithClient(ctx, logger, client)
			if (err != nil) != tt.expectErr {
				suite.T().Errorf("GetClusterIDWithClient() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if got != tt.want {
				suite.T().Errorf("GetClusterIDWithClient() got = %v, want %v", got, tt.want)
			}
		})
	}
}
