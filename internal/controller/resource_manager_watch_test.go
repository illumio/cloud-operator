package controller

import (
    "context"
    "testing"
    "time"

    "go.uber.org/zap"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
    "k8s.io/apimachinery/pkg/runtime"
    "k8s.io/apimachinery/pkg/watch"
    dynamicfake "k8s.io/client-go/dynamic/fake"
    clientgotesting "k8s.io/client-go/testing"

    pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// helper to build a basic Unstructured Pod with the provided name/ns/rv
func makeUnstructuredPod(name, namespace, rv string) *unstructured.Unstructured {
    obj := &unstructured.Unstructured{}
    obj.SetAPIVersion("v1")
    obj.SetKind("Namespace")
    obj.SetName(name)
    obj.SetNamespace(namespace)
    obj.SetResourceVersion(rv)
    return obj
}

func TestWatchEvents_EmitsMutations_ForAddModifyDelete_AndStopsOnError(t *testing.T) {
    // Arrange
    logger := zap.NewNop()

    scheme := runtime.NewScheme()
    dyn := dynamicfake.NewSimpleDynamicClient(scheme)

    // Use a FakeWatcher we control
    fw := watch.NewFake()
    dyn.Fake.PrependWatchReactor("*", func(action clientgotesting.Action) (handled bool, ret watch.Interface, err error) {
        return true, fw, nil
    })

    rm := &ResourceManager{
        resourceName:  "namespaces", // core/v1
        clientset:     nil,
        logger:        logger,
        dynamicClient: dyn,
        streamManager: &streamManager{}, // createMutationObject is all we need
    }

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    mutationCh := make(chan *pb.KubernetesResourceMutation, 10)

    // Run watcher in background
    errCh := make(chan error, 1)
    go func() {
        err := rm.watchEvents(ctx, "", metav1.ListOptions{Watch: true, ResourceVersion: "10"}, mutationCh)
        errCh <- err
    }()

    // Act: send events
    fw.Add(makeUnstructuredPod("p1", "default", "11"))
    fw.Modify(makeUnstructuredPod("p1", "default", "12"))
    fw.Delete(makeUnstructuredPod("p1", "default", "13"))

    // Bookmark should be ignored but advance RV
    bookmark := makeUnstructuredPod("p1", "default", "14")
    fw.Action(watch.Bookmark, bookmark)

    // Finally, send an error and expect the watcher to stop with error
    fw.Action(watch.Error, &metav1.Status{Code: 500, Reason: "InternalError", Message: "boom"})

    // Assert: we should receive three mutations in order
    expect := []string{"create", "update", "delete"}
    for i, want := range expect {
        select {
        case m := <-mutationCh:
            switch want {
            case "create":
                if m.GetCreateResource() == nil {
                    t.Fatalf("mutation %d: expected CreateResource, got %#v", i, m)
                }
            case "update":
                if m.GetUpdateResource() == nil {
                    t.Fatalf("mutation %d: expected UpdateResource, got %#v", i, m)
                }
            case "delete":
                if m.GetDeleteResource() == nil {
                    t.Fatalf("mutation %d: expected DeleteResource, got %#v", i, m)
                }
            }
        case <-time.After(3 * time.Second):
            t.Fatalf("timed out waiting for %s mutation", want)
        }
    }

    // And then the goroutine should end with an error due to the error event
    select {
    case err := <-errCh:
        if err == nil {
            t.Fatalf("expected error from watchEvents after error event, got nil")
        }
    case <-time.After(3 * time.Second):
        t.Fatalf("timed out waiting for watchEvents to return after error event")
    }
}

func TestGetErrFromWatchEvent(t *testing.T) {
    // Non-error type should be nil
    if err := getErrFromWatchEvent(watch.Event{Type: watch.Added}); err != nil {
        t.Fatalf("expected nil, got %v", err)
    }

    // Error with Status payload should be formatted
    st := &metav1.Status{Code: 404, Reason: "NotFound", Message: "nope"}
    err := getErrFromWatchEvent(watch.Event{Type: watch.Error, Object: st})
    if err == nil {
        t.Fatalf("expected error, got nil")
    }
}
