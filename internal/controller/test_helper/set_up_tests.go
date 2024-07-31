//Copyright 2024 Illumio, Inc. All Rights Reserved.

package test_helper

import (
	"os"
	"os/exec"
	"path/filepath"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// setupTestCluster creates a new KIND cluster for testing.
func SetupTestCluster() error {
	cmd := exec.Command("kind", "create", "cluster", "--name", "my-test-cluster", "--config", "../../kind-config.yaml")
	return cmd.Run()
}

// tearDownTestCluster destroys the KIND test cluster.
func TearDownTestCluster() error {
	cmd := exec.Command("kind", "delete", "cluster", "--name", "my-test-cluster")
	return cmd.Run()
}

// NewClientSet returns a new Kubernetes clientset based on the execution environment.
func NewClientSet() (*kubernetes.Clientset, error) {
	var clusterConfig *rest.Config
	var err error

	if os.Getenv("KUBECONFIG") != "" || !IsRunningInCluster() {
		var kubeconfig string
		if home := homedir.HomeDir(); home != "" {
			kubeconfig = filepath.Join(home, ".kube", "config")
		}
		clusterConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		clusterConfig, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, err
	}

	return kubernetes.NewForConfig(clusterConfig)
}

// IsRunningInCluster helps determine if the application is running inside a Kubernetes cluster.
func IsRunningInCluster() bool {
	// This can be based on the existence of a service account token, environment variables, or similar.
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}
