// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// KubernetesClient abstracts Kubernetes API operations.
type KubernetesClient interface {
	// GetClientset returns the underlying kubernetes.Interface
	GetClientset() kubernetes.Interface

	// GetDynamicClient returns the dynamic client for unstructured resources
	GetDynamicClient() dynamic.Interface

	// GetDiscoveryClient returns the discovery client for API discovery
	GetDiscoveryClient() discovery.DiscoveryInterface

	// GetSecret retrieves a secret by name from the specified namespace
	GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error)

	// CreateSecret creates a new secret in the specified namespace
	CreateSecret(ctx context.Context, namespace string, secret *corev1.Secret) (*corev1.Secret, error)

	// UpdateSecret updates an existing secret
	UpdateSecret(ctx context.Context, namespace string, secret *corev1.Secret) (*corev1.Secret, error)

	// ListResources lists resources of a given type
	ListResources(ctx context.Context, gvr schema.GroupVersionResource, namespace string) (*unstructured.UnstructuredList, error)

	// WatchResources watches for changes to resources of a given type
	WatchResources(ctx context.Context, gvr schema.GroupVersionResource, namespace string, resourceVersion string) (watch.Interface, error)
}

// LogStream abstracts the SendLogs gRPC stream.
type LogStream interface {
	Send(req *pb.SendLogsRequest) error
	Recv() (*pb.SendLogsResponse, error)
}

// ConfigStream abstracts the GetConfigurationUpdates gRPC stream.
type ConfigStream interface {
	Send(req *pb.GetConfigurationUpdatesRequest) error
	Recv() (*pb.GetConfigurationUpdatesResponse, error)
}

// ResourceStream abstracts the SendKubernetesResources gRPC stream.
type ResourceStream interface {
	Send(req *pb.SendKubernetesResourcesRequest) error
	Recv() (*pb.SendKubernetesResourcesResponse, error)
}

// NetworkFlowsStream abstracts the SendKubernetesNetworkFlows gRPC stream.
type NetworkFlowsStream interface {
	Send(req *pb.SendKubernetesNetworkFlowsRequest) error
	Recv() (*pb.SendKubernetesNetworkFlowsResponse, error)
}

// Clock abstracts time operations.
type Clock interface {
	// Now returns the current time
	Now() time.Time

	// Since returns the time elapsed since t
	Since(t time.Time) time.Duration

	// NewTimer creates a new Timer that will send the current time on its channel after at least duration d
	NewTimer(d time.Duration) Timer

	// NewTicker creates a new Ticker that sends the current time on its channel with a period specified by the duration
	NewTicker(d time.Duration) Ticker

	// After returns a channel that receives the current time after at least duration d
	After(d time.Duration) <-chan time.Time
}

// Timer represents a timer that can be stopped and reset.
type Timer interface {
	// C returns the channel on which the time is delivered
	C() <-chan time.Time

	// Stop prevents the Timer from firing
	Stop() bool

	// Reset changes the timer to expire after duration d
	Reset(d time.Duration) bool
}

// Ticker represents a ticker that delivers ticks at regular intervals.
type Ticker interface {
	// C returns the channel on which ticks are delivered
	C() <-chan time.Time

	// Stop stops the ticker
	Stop()
}

// realClock implements Clock using the standard time package.
type realClock struct{}

// NewRealClock returns a Clock implementation using the standard time package.
func NewRealClock() Clock {
	return &realClock{}
}

func (c *realClock) Now() time.Time {
	return time.Now()
}

func (c *realClock) Since(t time.Time) time.Duration {
	return time.Since(t)
}

func (c *realClock) NewTimer(d time.Duration) Timer {
	return &realTimer{timer: time.NewTimer(d)}
}

func (c *realClock) NewTicker(d time.Duration) Ticker {
	return &realTicker{ticker: time.NewTicker(d)}
}

func (c *realClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

type realTimer struct {
	timer *time.Timer
}

func (t *realTimer) C() <-chan time.Time {
	return t.timer.C
}

func (t *realTimer) Stop() bool {
	return t.timer.Stop()
}

func (t *realTimer) Reset(d time.Duration) bool {
	return t.timer.Reset(d)
}

type realTicker struct {
	ticker *time.Ticker
}

func (t *realTicker) C() <-chan time.Time {
	return t.ticker.C
}

func (t *realTicker) Stop() {
	t.ticker.Stop()
}
