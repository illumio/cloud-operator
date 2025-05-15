package controller

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// SynchronousDeleteNamespace deletes a namespace and blocks until it's deleted
// or timeout. If it didn't exist upon first call, then return immediately
func (s *ControllerTestSuite) SynchronousDeleteNamespace(namespace string, timeout time.Duration) {
	s.T().Logf("Requesting deletion of namespace: %q", namespace)

	gracePeriod := int64(0)
	err := s.clientset.CoreV1().Namespaces().
		Delete(s.ctx, namespace, metav1.DeleteOptions{GracePeriodSeconds: &gracePeriod})
	if err != nil && !errors.IsNotFound(err) {
		s.T().Fatalf("Failed to delete namespace %q: %v", namespace, err)
	}

	watcher, err := s.clientset.CoreV1().Namespaces().
		Watch(s.ctx, metav1.ListOptions{FieldSelector: "metadata.name=" + namespace})
	if err != nil {
		s.T().Fatalf("Failed to set up watch for namespace %q: %v", namespace, err)
	}
	defer watcher.Stop()

	s.waitForDeletion(watcher, "namespace", namespace, timeout)
}

// SynchronousDeleteService deletes a service and blocks until it's deleted or
// timeout. If it didn't exist upon first call, then return immediately
func (s *ControllerTestSuite) SynchronousDeleteService(
	namespace, name string,
	timeout time.Duration,
) {
	s.T().Logf("Requesting deletion of service: %q in namespace %q", name, namespace)

	gracePeriod := int64(0)
	err := s.clientset.CoreV1().Services(namespace).
		Delete(s.ctx, name, metav1.DeleteOptions{GracePeriodSeconds: &gracePeriod})
	if err != nil && !errors.IsNotFound(err) {
		s.T().Fatalf("Failed to delete service %q: %v", name, err)
	}

	watcher, err := s.clientset.CoreV1().Services(namespace).
		Watch(s.ctx, metav1.ListOptions{FieldSelector: "metadata.name=" + name})
	if err != nil {
		s.T().Fatalf("Failed to set up watch for service %q: %v", name, err)
	}
	defer watcher.Stop()

	s.waitForDeletion(watcher, "service", name, timeout)
}

// waitForDeletion watches for a Deleted event or timeout.
func (s *ControllerTestSuite) waitForDeletion(
	watcher watch.Interface,
	kind, name string,
	timeout time.Duration,
) {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	for {
		select {
		case event, ok := <-watcher.ResultChan():
			if !ok {
				s.T().Fatalf("Watch closed unexpectedly while waiting for %s %q to be deleted", kind, name)
				return
			}

			if event.Type == watch.Deleted {
				s.T().Logf("successfully deleted %s %q", kind, name)
				return
			}

		case <-ctx.Done():
			s.T().Fatalf("Context done while waiting for %s %q to be deleted: %v", kind, name, ctx.Err())
			return
		}
	}
}
