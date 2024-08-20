package controller

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsRunningInCluster(t *testing.T) {
	t.Run("Running in cluster", func(t *testing.T) {
		os.Setenv("KUBERNETES_SERVICE_HOST", "localhost")
		defer os.Unsetenv("KUBERNETES_SERVICE_HOST")

		assert.True(t, IsRunningInCluster())
	})

	t.Run("Not running in cluster", func(t *testing.T) {
		os.Unsetenv("KUBERNETES_SERVICE_HOST")

		assert.False(t, IsRunningInCluster())
	})
}