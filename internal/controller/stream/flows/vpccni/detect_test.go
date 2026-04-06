// Copyright 2026 Illumio, Inc. All Rights Reserved.

package vpccni

import (
	"context"
	"testing"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestIsVPCCNIAvailable(t *testing.T) {
	logger := zap.NewNop()
	ctx := context.Background()

	tests := []struct {
		name     string
		pods     []corev1.Pod
		expected bool
	}{
		{
			name:     "no aws-node pods",
			pods:     []corev1.Pod{},
			expected: false,
		},
		{
			name: "aws-node pod without nodeagent container",
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "aws-node-abc",
						Namespace: "kube-system",
						Labels: map[string]string{
							"k8s-app": "aws-node",
						},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "aws-node"},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "aws-node pod with nodeagent container",
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "aws-node-xyz",
						Namespace: "kube-system",
						Labels: map[string]string{
							"k8s-app": "aws-node",
						},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "aws-node"},
							{Name: "aws-eks-nodeagent"},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "multiple aws-node pods with nodeagent",
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "aws-node-aaa",
						Namespace: "kube-system",
						Labels: map[string]string{
							"k8s-app": "aws-node",
						},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "aws-node"},
							{Name: "aws-eks-nodeagent"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "aws-node-bbb",
						Namespace: "kube-system",
						Labels: map[string]string{
							"k8s-app": "aws-node",
						},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "aws-node"},
							{Name: "aws-eks-nodeagent"},
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			for _, pod := range tt.pods {
				_, err := client.CoreV1().Pods(pod.Namespace).Create(ctx, &pod, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("failed to create pod: %v", err)
				}
			}

			result := IsVPCCNIAvailable(ctx, logger, client)
			if result != tt.expected {
				t.Errorf("IsVPCCNIAvailable() = %v, want %v", result, tt.expected)
			}
		})
	}
}
