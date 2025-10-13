// Copyright 2024 Illumio, Inc. All Rights Reserved.

package testhelper

import (
	"context"
	"os/exec"
)

// SetupTestCluster creates a new KIND cluster for testing.
func SetupTestCluster() error {
	cmd := exec.CommandContext(context.Background(), "kind", "create", "cluster", "--name", "my-test-cluster", "--config", "../../kind-config.yaml")

	return cmd.Run()
}

// TearDownTestCluster destroys the KIND test cluster.
func TearDownTestCluster() error {
	cmd := exec.CommandContext(context.Background(), "kind", "delete", "cluster", "--name", "my-test-cluster")

	return cmd.Run()
}
