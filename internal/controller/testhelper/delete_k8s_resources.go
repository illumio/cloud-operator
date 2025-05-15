package deleter

import (
    "context"
    "testing"

    "github.com/openlyinc/pointy"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/watch"
    "k8s.io/client-go/kubernetes"
    "k8s.io/apimachinery/pkg/api/errors"
)

// SynchronousDeleter encapsulates synchronous deletion logic.
type SynchronousDeleter struct {
    T         *testing.T
    Ctx       context.Context
    Clientset kubernetes.Interface
}

// DeleteNamespace deletes a namespace and blocks until it's deleted or context ends.
func (d *SynchronousDeleter) DeleteNamespace(namespace string) {
    d.T.Logf("Requesting deletion of namespace: %q", namespace)
    err := d.Clientset.CoreV1().Namespaces().Delete(d.Ctx, namespace, metav1.DeleteOptions{})
    if err != nil && !errors.IsNotFound(err) {
        d.T.Fatalf("Failed to delete namespace %q: %v", namespace, err)
    }

    watcher, err := d.Clientset.CoreV1().Namespaces().Watch(d.Ctx, metav1.ListOptions{
        FieldSelector:  "metadata.name=" + namespace,
    })
    if err != nil {
        d.T.Fatalf("Failed to set up watch for namespace %q: %v", namespace, err)
    }
    defer watcher.Stop()

    d.waitForDeletion(watcher, "namespace", namespace)
}

// DeleteService deletes a service and blocks until it's deleted or context ends.
func (d *SynchronousDeleter) DeleteService(namespace, name string) {
    d.T.Logf("Requesting deletion of service: %q in namespace %q", name, namespace)
    err := d.Clientset.CoreV1().Services(namespace).Delete(d.Ctx, name, metav1.DeleteOptions{})
    if err != nil && !errors.IsNotFound(err) {
        d.T.Fatalf("Failed to delete service %q: %v", name, err)
    }

    watcher, err := d.Clientset.CoreV1().Services(namespace).Watch(d.Ctx, metav1.ListOptions{
        FieldSelector:  "metadata.name=" + name,
    })
    if err != nil {
        d.T.Fatalf("Failed to set up watch for service %q: %v", name, err)
    }
    defer watcher.Stop()

    d.waitForDeletion(watcher, "service", name)
}

// waitForDeletion watches for a Deleted event or exits when context ends.
func (d *SynchronousDeleter) waitForDeletion(watcher watch.Interface, kind, name string) {
    for {
        select {
        case event, ok := <-watcher.ResultChan():
            if !ok {
                d.T.Fatalf("Watch closed unexpectedly while waiting for %s %q to be deleted", kind, name)
                return
            }

            if event.Type == watch.Deleted {
                d.T.Logf("%s %q successfully deleted", kind, name)
                return
            }

        case <-d.Ctx.Done():
            d.T.Fatalf("Context done while waiting for %s %q to be deleted: %v", kind, name, d.Ctx.Err())
            return
        }
    }
}
