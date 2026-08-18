// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func node(name string, labels map[string]string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
	}
}

func TestIsEKSAutoModeAvailable_AutoNodePresent(t *testing.T) {
	client := fake.NewSimpleClientset(
		node("std", map[string]string{"kubernetes.io/os": "linux"}),
		node("auto", map[string]string{EKSComputeTypeLabel: EKSComputeTypeAuto}),
	)

	assert.True(t, IsEKSAutoModeAvailable(context.Background(), zap.NewNop(), client))
}

func TestIsEKSAutoModeAvailable_NoAutoNode(t *testing.T) {
	client := fake.NewSimpleClientset(
		node("std1", map[string]string{"kubernetes.io/os": "linux"}),
		node("std2", nil),
	)

	assert.False(t, IsEKSAutoModeAvailable(context.Background(), zap.NewNop(), client))
}

func TestIsEKSAutoModeAvailable_NoNodes(t *testing.T) {
	client := fake.NewSimpleClientset()

	assert.False(t, IsEKSAutoModeAvailable(context.Background(), zap.NewNop(), client))
}

func TestNetworkPolicyAgentLogPathSegments_Default(t *testing.T) {
	assert.Equal(t,
		[]string{"logs", "aws-routed-eni", "network-policy-agent.log"},
		NetworkPolicyAgentLogPathSegments(""),
	)
}

func TestNetworkPolicyAgentLogPathSegments_Override(t *testing.T) {
	assert.Equal(t,
		[]string{"logs", "custom", "path", "agent.log"},
		NetworkPolicyAgentLogPathSegments("custom/path/agent.log"),
	)
}

func TestNetworkPolicyAgentLogPathSegments_StripsEmptySegments(t *testing.T) {
	assert.Equal(t,
		[]string{"logs", "a", "b.log"},
		NetworkPolicyAgentLogPathSegments("/a//b.log"),
	)
}
