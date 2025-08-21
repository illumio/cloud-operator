// Copyright 2024 Illumio, Inc. All Rights Reserved.

package testhelper

import (
	"context"
	"os/exec"
)

// setupTestCluster creates a new KIND cluster for testing.
func SetupTestCluster() error {
	cmd := exec.CommandContext(context.Background(), "kind", "create", "cluster", "--name", "my-test-cluster", "--config", "../../kind-config.yaml")

	return cmd.Run()
}

// tearDownTestCluster destroys the KIND test cluster.
func TearDownTestCluster() error {
	cmd := exec.CommandContext(context.Background(), "kind", "delete", "cluster", "--name", "my-test-cluster")

	return cmd.Run()
}
