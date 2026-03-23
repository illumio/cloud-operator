// Copyright 2026 Illumio, Inc. All Rights Reserved.

package k8sclient

import (
	"context"
	"net/http"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// Client abstracts Kubernetes API operations.
type Client interface {
	// GetClientset returns the underlying kubernetes.Interface.
	GetClientset() kubernetes.Interface

	// GetDynamicClient returns the dynamic client for unstructured resources.
	GetDynamicClient() dynamic.Interface

	// GetDiscoveryClient returns the discovery client for API discovery.
	GetDiscoveryClient() discovery.DiscoveryInterface

	// GetSecret retrieves a secret by name from the specified namespace.
	GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error)

	// CreateSecret creates a new secret in the specified namespace.
	CreateSecret(ctx context.Context, namespace string, secret *corev1.Secret) (*corev1.Secret, error)

	// UpdateSecret updates an existing secret.
	UpdateSecret(ctx context.Context, namespace string, secret *corev1.Secret) (*corev1.Secret, error)

	// ListResources lists resources of a given type.
	ListResources(ctx context.Context, gvr schema.GroupVersionResource, namespace string) (*unstructured.UnstructuredList, error)

	// WatchResources watches for changes to resources of a given type.
	WatchResources(ctx context.Context, gvr schema.GroupVersionResource, namespace string, resourceVersion string) (watch.Interface, error)
}

// realClient implements Client using actual K8s clients.
type realClient struct {
	clientset     kubernetes.Interface
	dynamicClient dynamic.Interface
	config        *rest.Config
}

// disableProxyTransport wraps the config to disable HTTP proxy.
// This ensures consistent behavior with NewClientSet which also disables proxy.
func disableProxyTransport(config *rest.Config) {
	config.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		if transport, ok := rt.(*http.Transport); ok {
			customTransport := transport.Clone()
			customTransport.Proxy = nil

			return customTransport
		}

		return rt
	}
}

// IsRunningInCluster returns true if the application is running inside a Kubernetes cluster.
func IsRunningInCluster() bool {
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

// NewClient creates a new Client that uses real K8s APIs.
// It supports both in-cluster and out-of-cluster (kubeconfig) execution.
func NewClient() (Client, error) {
	config, err := getKubeConfig()
	if err != nil {
		return nil, err
	}

	return NewClientFromConfig(config)
}

// getKubeConfig returns a Kubernetes config based on the execution environment.
// It uses kubeconfig if KUBECONFIG is set or running outside a cluster,
// otherwise it uses in-cluster config.
func getKubeConfig() (*rest.Config, error) {
	if os.Getenv("KUBECONFIG") != "" || !IsRunningInCluster() {
		var kubeconfig string
		if home := homedir.HomeDir(); home != "" {
			kubeconfig = filepath.Join(home, ".kube", "config")
		}

		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}

	return rest.InClusterConfig()
}

// NewClientFromConfig creates a Client from an existing config.
func NewClientFromConfig(config *rest.Config) (Client, error) {
	disableProxyTransport(config)

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &realClient{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		config:        config,
	}, nil
}

// NewClientFromClients creates a Client from existing clients.
func NewClientFromClients(clientset kubernetes.Interface, dynamicClient dynamic.Interface) Client {
	return &realClient{
		clientset:     clientset,
		dynamicClient: dynamicClient,
	}
}

func (c *realClient) GetClientset() kubernetes.Interface {
	return c.clientset
}

func (c *realClient) GetDynamicClient() dynamic.Interface {
	return c.dynamicClient
}

func (c *realClient) GetDiscoveryClient() discovery.DiscoveryInterface {
	return c.clientset.Discovery()
}

func (c *realClient) GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	return c.clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (c *realClient) CreateSecret(ctx context.Context, namespace string, secret *corev1.Secret) (*corev1.Secret, error) {
	return c.clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
}

func (c *realClient) UpdateSecret(ctx context.Context, namespace string, secret *corev1.Secret) (*corev1.Secret, error) {
	return c.clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
}

func (c *realClient) ListResources(ctx context.Context, gvr schema.GroupVersionResource, namespace string) (*unstructured.UnstructuredList, error) {
	if namespace == "" {
		return c.dynamicClient.Resource(gvr).List(ctx, metav1.ListOptions{})
	}

	return c.dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
}

func (c *realClient) WatchResources(ctx context.Context, gvr schema.GroupVersionResource, namespace string, resourceVersion string) (watch.Interface, error) {
	opts := metav1.ListOptions{
		ResourceVersion: resourceVersion,
		Watch:           true,
	}
	if namespace == "" {
		return c.dynamicClient.Resource(gvr).Watch(ctx, opts)
	}

	return c.dynamicClient.Resource(gvr).Namespace(namespace).Watch(ctx, opts)
}
