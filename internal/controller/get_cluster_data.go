// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"

	"go.uber.org/zap"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GetClusterID returns the uid of the k8s cluster's kube-system namespace, which is used as the cluster's globally unique ID.
func GetClusterID(ctx context.Context, logger *zap.SugaredLogger) (string, error) {
	clientset, err := NewClientSet()
	if err != nil {
		logger.Error(err, "Error creating clientset")
	}
	namespace, err := clientset.CoreV1().Namespaces().Get(ctx, "kube-system", v1.GetOptions{})
	if err != nil {
		logger.Error(err, "Could not find kube-system namespace")
	}
	return string(namespace.UID), nil
}
