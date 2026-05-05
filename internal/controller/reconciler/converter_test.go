// Copyright 2026 Illumio, Inc. All Rights Reserved.

package reconciler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestGetCloudSecureID(t *testing.T) {
	tests := []struct {
		name     string
		obj      *unstructured.Unstructured
		expected string
	}{
		{
			name: "object with CloudSecure ID",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"metadata": map[string]any{
						"annotations": map[string]any{
							id: "my-id",
						},
					},
				},
			},
			expected: "my-id",
		},
		{
			name: "object without annotations",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"metadata": map[string]any{
						"name": "test",
					},
				},
			},
			expected: "",
		},
		{
			name: "object with other annotations but no CloudSecure ID",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"metadata": map[string]any{
						"annotations": map[string]any{
							"other": "value",
						},
					},
				},
			},
			expected: "",
		},
		{
			name:     "empty object",
			obj:      &unstructured.Unstructured{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetCloudSecureID(tt.obj)
			assert.Equal(t, tt.expected, result)
		})
	}
}

