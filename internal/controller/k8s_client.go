// Copyright 2026 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// KubernetesClient abstracts Kubernetes API operations.
type KubernetesClient interface {
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

// realKubernetesClient implements KubernetesClient using actual K8s clients.
type realKubernetesClient struct {
	clientset     kubernetes.Interface
	dynamicClient dynamic.Interface
	config        *rest.Config
}

// NewRealKubernetesClient creates a new KubernetesClient that uses real K8s APIs.
func NewRealKubernetesClient() (KubernetesClient, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &realKubernetesClient{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		config:        config,
	}, nil
}

// NewRealKubernetesClientFromConfig creates a KubernetesClient from an existing config.
func NewRealKubernetesClientFromConfig(config *rest.Config) (KubernetesClient, error) {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &realKubernetesClient{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		config:        config,
	}, nil
}

// NewRealKubernetesClientFromClients creates a KubernetesClient from existing clients.
func NewRealKubernetesClientFromClients(clientset kubernetes.Interface, dynamicClient dynamic.Interface) KubernetesClient {
	return &realKubernetesClient{
		clientset:     clientset,
		dynamicClient: dynamicClient,
	}
}

func (c *realKubernetesClient) GetClientset() kubernetes.Interface {
	return c.clientset
}

func (c *realKubernetesClient) GetDynamicClient() dynamic.Interface {
	return c.dynamicClient
}

func (c *realKubernetesClient) GetDiscoveryClient() discovery.DiscoveryInterface {
	return c.clientset.Discovery()
}

func (c *realKubernetesClient) GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	return c.clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (c *realKubernetesClient) CreateSecret(ctx context.Context, namespace string, secret *corev1.Secret) (*corev1.Secret, error) {
	return c.clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
}

func (c *realKubernetesClient) UpdateSecret(ctx context.Context, namespace string, secret *corev1.Secret) (*corev1.Secret, error) {
	return c.clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
}

func (c *realKubernetesClient) ListResources(ctx context.Context, gvr schema.GroupVersionResource, namespace string) (*unstructured.UnstructuredList, error) {
	if namespace == "" {
		return c.dynamicClient.Resource(gvr).List(ctx, metav1.ListOptions{})
	}

	return c.dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
}

func (c *realKubernetesClient) WatchResources(ctx context.Context, gvr schema.GroupVersionResource, namespace string, resourceVersion string) (watch.Interface, error) {
	opts := metav1.ListOptions{
		ResourceVersion: resourceVersion,
		Watch:           true,
	}
	if namespace == "" {
		return c.dynamicClient.Resource(gvr).Watch(ctx, opts)
	}

	return c.dynamicClient.Resource(gvr).Namespace(namespace).Watch(ctx, opts)
}
