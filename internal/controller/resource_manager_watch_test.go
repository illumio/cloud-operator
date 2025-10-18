package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clientgotesting "k8s.io/client-go/testing"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// helper: minimal unstructured Namespace with name and rv
func makeUnstructuredNS(name, rv string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("v1")
	obj.SetKind("Namespace")
	obj.SetName(name)
	obj.SetNamespace("default")
	obj.SetResourceVersion(rv)

	return obj
}

func TestUpdateRVFromBookmark(t *testing.T) {
	// When bookmark contains RV, it should be returned
	u := makeUnstructuredNS("ns1", "42")

	ev := watch.Event{Type: watch.Bookmark, Object: u}
	if got, err := getResourceVersionFromBookmark(ev); err != nil || got != "42" {
		t.Fatalf("expected rv 42 with nil err, got rv=%q err=%v", got, err)
	}

	// When object is nil, last known should be preserved
	ev2 := watch.Event{Type: watch.Bookmark, Object: nil}
	if got, err := getResourceVersionFromBookmark(ev2); err == nil || got != "" {
		t.Fatalf("expected error for nil object and empty rv, got rv=%q err=%v", got, err)
	}

	// When object has empty RV, preserve last known
	u2 := makeUnstructuredNS("ns2", "")

	ev4 := watch.Event{Type: watch.Bookmark, Object: u2}
	if got, err := getResourceVersionFromBookmark(ev4); err == nil || got != "" {
		t.Fatalf("expected error for empty RV, got rv=%q err=%v", got, err)
	}
}

func TestNewWatcher_StopsExistingAndReturnsNew(t *testing.T) {
	logger := zap.NewNop()
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)

	// We will return a fresh Fake watcher from the reactor
	newWatcher := watch.NewFake()
	dyn.PrependWatchReactor("*", func(action clientgotesting.Action) (handled bool, ret watch.Interface, err error) {
		return true, newWatcher, nil
	})

	rm := &ResourceManager{
		resourceName:  "namespaces",
		dynamicClient: dyn,
		logger:        logger,
		streamManager: &streamManager{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// existing watcher that should be stopped by newWatcher
	existing := watch.NewFake()

	// Call newWatcher
	w, err := rm.newWatcher(ctx, "", "25", existing, logger)
	if err != nil {
		t.Fatalf("newWatcher returned error: %v", err)
	}

	if w == nil {
		t.Fatalf("expected non-nil watcher")
	}

	// Verify existing watcher was stopped (its channel should be closed)
	select {
	case _, ok := <-existing.ResultChan():
		if ok {
			t.Fatalf("expected existing watcher channel to be closed")
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for existing watcher to stop")
	}
}

func TestNewWatcher_PropagatesErrors(t *testing.T) {
	logger := zap.NewNop()
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)

	wantErr := errors.New("boom")

	dyn.PrependWatchReactor("*", func(action clientgotesting.Action) (handled bool, ret watch.Interface, err error) {
		return true, nil, wantErr
	})

	rm := &ResourceManager{
		resourceName:  "namespaces",
		dynamicClient: dyn,
		logger:        logger,
		streamManager: &streamManager{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := rm.newWatcher(ctx, "", "1", nil, logger)
	if err == nil {
		t.Fatalf("expected error from newWatcher, got nil")
	}
}

func TestProcessMutation_SendsCorrectMutationTypes(t *testing.T) {
	logger := zap.NewNop()
	rm := &ResourceManager{
		resourceName:  "namespaces",
		logger:        logger,
		streamManager: &streamManager{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Buffered channel to avoid blocking
	ch := make(chan *pb.KubernetesResourceMutation, 3)

	// Added → CreateResource
	u1 := makeUnstructuredNS("n1", "10")
	if err := rm.processMutation(ctx, watch.Event{Type: watch.Added, Object: u1}, ch, logger); err != nil {
		t.Fatalf("processMutation(Add) error: %v", err)
	}

	// Modified → UpdateResource
	u2 := makeUnstructuredNS("n1", "11")
	if err := rm.processMutation(ctx, watch.Event{Type: watch.Modified, Object: u2}, ch, logger); err != nil {
		t.Fatalf("processMutation(Modify) error: %v", err)
	}

	// Deleted → DeleteResource
	u3 := makeUnstructuredNS("n1", "12")
	if err := rm.processMutation(ctx, watch.Event{Type: watch.Deleted, Object: u3}, ch, logger); err != nil {
		t.Fatalf("processMutation(Delete) error: %v", err)
	}

	// Assert the three mutations
	// 1) Create
	m1 := <-ch
	if m1.GetCreateResource() == nil {
		t.Fatalf("expected CreateResource mutation, got %#v", m1)
	}
	// 2) Update
	m2 := <-ch
	if m2.GetUpdateResource() == nil {
		t.Fatalf("expected UpdateResource mutation, got %#v", m2)
	}
	// 3) Delete
	m3 := <-ch
	if m3.GetDeleteResource() == nil {
		t.Fatalf("expected DeleteResource mutation, got %#v", m3)
	}
}

func TestProcessMutation_RespectsContextCancellation(t *testing.T) {
	logger := zap.NewNop()
	rm := &ResourceManager{
		resourceName:  "namespaces",
		logger:        logger,
		streamManager: &streamManager{},
	}

	// Cancel before calling to force ctx.Err()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch := make(chan *pb.KubernetesResourceMutation)
	u := makeUnstructuredNS("n1", "10")

	err := rm.processMutation(ctx, watch.Event{Type: watch.Added, Object: u}, ch, logger)
	if err == nil {
		t.Fatalf("expected context error, got nil")
	}
}

func TestProcessMutation_ConstructsMetadataCorrectly(t *testing.T) {
	logger := zap.NewNop()
	rm := &ResourceManager{
		resourceName:  "namespaces",
		logger:        logger,
		streamManager: &streamManager{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan *pb.KubernetesResourceMutation, 3)

	const (
		nsKind    = "Namespace"
		defaultNS = "default"
	)

	// Prepare three events for the same object with increasing RVs
	u1 := makeUnstructuredNS("n1", "10")
	u2 := makeUnstructuredNS("n1", "11")
	u3 := makeUnstructuredNS("n1", "12")

	if err := rm.processMutation(ctx, watch.Event{Type: watch.Added, Object: u1}, ch, logger); err != nil {
		t.Fatalf("processMutation(Add) error: %v", err)
	}

	if err := rm.processMutation(ctx, watch.Event{Type: watch.Modified, Object: u2}, ch, logger); err != nil {
		t.Fatalf("processMutation(Modify) error: %v", err)
	}

	if err := rm.processMutation(ctx, watch.Event{Type: watch.Deleted, Object: u3}, ch, logger); err != nil {
		t.Fatalf("processMutation(Delete) error: %v", err)
	}

	// Validate metadata contents
	if got := (<-ch).GetCreateResource(); got == nil {
		t.Fatalf("expected CreateResource mutation")
	} else if got.GetKind() != nsKind || got.GetName() != "n1" || got.GetNamespace() != defaultNS || got.GetResourceVersion() != "10" {
		t.Fatalf("unexpected metadata in CreateResource: %#v", got)
	}

	if got := (<-ch).GetUpdateResource(); got == nil {
		t.Fatalf("expected UpdateResource mutation")
	} else if got.GetKind() != nsKind || got.GetName() != "n1" || got.GetNamespace() != defaultNS || got.GetResourceVersion() != "11" {
		t.Fatalf("unexpected metadata in UpdateResource: %#v", got)
	}

	if got := (<-ch).GetDeleteResource(); got == nil {
		t.Fatalf("expected DeleteResource mutation")
	} else if got.GetKind() != nsKind || got.GetName() != "n1" || got.GetNamespace() != defaultNS || got.GetResourceVersion() != "12" {
		t.Fatalf("unexpected metadata in DeleteResource: %#v", got)
	}
}
