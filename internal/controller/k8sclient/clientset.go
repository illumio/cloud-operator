// Copyright 2026 Illumio, Inc. All Rights Reserved.

package k8sclient

import (
	"net/http"
	"os"
	"path/filepath"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// IsRunningInCluster helps determine if the application is running inside a Kubernetes cluster.
func IsRunningInCluster() bool {
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

// newClientForConfig contains only the transport wrapping logic to avoid http proxy env variable.
func newClientForConfig(config *rest.Config) (*kubernetes.Clientset, error) {
	// Use WrapTransport to customize the transport for this specific client.
	config.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		if transport, ok := rt.(*http.Transport); ok {
			// Clone the transport to avoid modifying a shared one.
			customTransport := transport.Clone()
			// Explicitly disable the proxy.
			customTransport.Proxy = nil

			return customTransport
		}

		return rt
	}

	return kubernetes.NewForConfig(config)
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

	return newClientForConfig(clusterConfig)
}
