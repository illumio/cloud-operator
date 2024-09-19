// Copyright 2024 Illumio, Inc. All Rights Reserved.

package testhelper

import (
	"fmt"
	"os/exec"
)

// setupTestCluster creates a new KIND cluster for testing.
func SetupTestCluster() error {
	cmd := exec.Command("kind", "create", "cluster", "--name", "my-test-cluster", "--config", "../../kind-config.yaml")
	fmt.Println(cmd)
	return cmd.Run()
}

// tearDownTestCluster destroys the KIND test cluster.
func TearDownTestCluster() error {
	cmd := exec.Command("kind", "delete", "cluster", "--name", "my-test-cluster")
	return cmd.Run()
}
