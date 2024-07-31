// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// Mocked function to replace the real GetClusterID function for testing
func GetClusterIDWithClient(ctx context.Context, logger logr.Logger, clientset *fake.Clientset) (string, error) {
	namespace, err := clientset.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
	if err != nil {
		logger.Error(err, "Failed to get kube-system namespace")
		return "", err
	}
	return string(namespace.UID), nil
}

func TestGetClusterID(t *testing.T) {
	ctx := context.Background()
	logger := funcr.New(func(prefix, args string) {
		t.Logf("%s%s", prefix, args)
	}, funcr.Options{})

	tests := []struct {
		name      string
		setup     func() *fake.Clientset
		want      string
		expectErr bool
	}{
		{
			name: "Successful retrieval of cluster ID",
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
		{
			name: "Namespace not found",
			setup: func() *fake.Clientset {
				client := fake.NewSimpleClientset()
				return client
			},
			want:      "",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := tt.setup()
			got, err := GetClusterIDWithClient(ctx, logger, client)
			if (err != nil) != tt.expectErr {
				t.Errorf("GetClusterIDWithClient() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetClusterIDWithClient() got = %v, want %v", got, tt.want)
			}
		})
	}
}
