// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestIsCalicoEnterpriseDeployed(t *testing.T) {
	installation := func(name string, spec map[string]any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "operator.tigera.io/v1",
			"kind":       "Installation",
			"metadata":   map[string]any{"name": name},
			"spec":       spec,
		}}
	}

	tests := map[string]struct {
		objects        []runtime.Object
		expectedResult bool
	}{
		"Installation with the Enterprise variant": {
			objects:        []runtime.Object{installation("default", map[string]any{"variant": "TigeraSecureEnterprise"})},
			expectedResult: true,
		},
		"Installation with the open-source Calico variant": {
			objects:        []runtime.Object{installation("default", map[string]any{"variant": "Calico"})},
			expectedResult: false,
		},
		"Installation without a variant": {
			objects:        []runtime.Object{installation("default", map[string]any{})},
			expectedResult: false,
		},
		"Enterprise Installation with a different name is ignored": {
			objects:        []runtime.Object{installation("other", map[string]any{"variant": "TigeraSecureEnterprise"})},
			expectedResult: false,
		},
		"no Installation": {
			expectedResult: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
				map[schema.GroupVersionResource]string{calicoInstallationGVR: "InstallationList"},
				tt.objects...)

			result := IsCalicoEnterpriseDeployed(context.TODO(), zap.NewNop(), dynamicClient)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}
