// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clientgotesting "k8s.io/client-go/testing"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// helper: minimal unstructured Namespace with name and rv
func newUnstructuredNamespace(name, rv string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("v1")
	obj.SetKind("Namespace")
	obj.SetName(name)
	obj.SetNamespace("default")
	obj.SetResourceVersion(rv)

	return obj
}

func TestUpdateResourceVersionFromBookmark(t *testing.T) {
	u := newUnstructuredNamespace("ns1", "42")

	ev := watch.Event{Type: watch.Bookmark, Object: u}
	if got, err := getResourceVersionFromBookmark(ev); err != nil || got != "42" {
		t.Fatalf("expected rv 42 with nil err, got rv=%q err=%v", got, err)
	}

	ev2 := watch.Event{Type: watch.Bookmark, Object: nil}
	if got, err := getResourceVersionFromBookmark(ev2); err == nil || got != "" {
		t.Fatalf("expected error for nil object and empty rv, got rv=%q err=%v", got, err)
	}

	u2 := newUnstructuredNamespace("ns2", "")

	ev4 := watch.Event{Type: watch.Bookmark, Object: u2}
	if got, err := getResourceVersionFromBookmark(ev4); err == nil || got != "" {
		t.Fatalf("expected error for empty RV, got rv=%q err=%v", got, err)
	}
}

func TestNewWatcher_StopsExistingAndReturnsNew(t *testing.T) {
	logger := zap.NewNop()
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)

	newWatcher := watch.NewFake()
	dyn.PrependWatchReactor("*", func(action clientgotesting.Action) (handled bool, ret watch.Interface, err error) {
		return true, newWatcher, nil
	})

	rm := &Watcher{
		resourceName:  "namespaces",
		dynamicClient: dyn,
		logger:        logger,
		streamManager: &stream.Manager{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, err := rm.newWatcher(ctx, "25", logger)
	if err != nil {
		t.Fatalf("newWatcher returned error: %v", err)
	}

	if w == nil {
		t.Fatalf("expected non-nil watcher")
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

	rm := &Watcher{
		resourceName:  "namespaces",
		dynamicClient: dyn,
		logger:        logger,
		streamManager: &stream.Manager{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := rm.newWatcher(ctx, "1", logger)
	if err == nil {
		t.Fatalf("expected error from newWatcher, got nil")
	}
}

func TestProcessMutation_SendsCorrectMutationTypes(t *testing.T) {
	logger := zap.NewNop()
	rm := &Watcher{
		resourceName:  "namespaces",
		logger:        logger,
		streamManager: &stream.Manager{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan *pb.KubernetesResourceMutation, 3)

	u1 := newUnstructuredNamespace("n1", "10")
	if _, err := rm.processMutation(ctx, watch.Event{Type: watch.Added, Object: u1}, ch); err != nil {
		t.Fatalf("processMutation(Add) error: %v", err)
	}

	u2 := newUnstructuredNamespace("n1", "11")
	if _, err := rm.processMutation(ctx, watch.Event{Type: watch.Modified, Object: u2}, ch); err != nil {
		t.Fatalf("processMutation(Modify) error: %v", err)
	}

	u3 := newUnstructuredNamespace("n1", "12")
	if _, err := rm.processMutation(ctx, watch.Event{Type: watch.Deleted, Object: u3}, ch); err != nil {
		t.Fatalf("processMutation(Delete) error: %v", err)
	}

	m1 := <-ch
	if m1.GetCreateResource() == nil {
		t.Fatalf("expected CreateResource mutation, got %#v", m1)
	}

	m2 := <-ch
	if m2.GetUpdateResource() == nil {
		t.Fatalf("expected UpdateResource mutation, got %#v", m2)
	}

	m3 := <-ch
	if m3.GetDeleteResource() == nil {
		t.Fatalf("expected DeleteResource mutation, got %#v", m3)
	}
}

func TestProcessMutation_RespectsContextCancellation(t *testing.T) {
	logger := zap.NewNop()
	rm := &Watcher{
		resourceName:  "namespaces",
		logger:        logger,
		streamManager: &stream.Manager{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch := make(chan *pb.KubernetesResourceMutation)
	u := newUnstructuredNamespace("n1", "10")

	_, err := rm.processMutation(ctx, watch.Event{Type: watch.Added, Object: u}, ch)
	if err == nil {
		t.Fatalf("expected context error, got nil")
	}
}

func TestProcessMutation_ConstructsMetadataCorrectly(t *testing.T) {
	logger := zap.NewNop()
	rm := &Watcher{
		resourceName:  "namespaces",
		logger:        logger,
		streamManager: &stream.Manager{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan *pb.KubernetesResourceMutation, 3)

	const (
		nsKind    = "Namespace"
		defaultNS = "default"
	)

	u1 := newUnstructuredNamespace("n1", "10")
	u2 := newUnstructuredNamespace("n1", "11")
	u3 := newUnstructuredNamespace("n1", "12")

	if _, err := rm.processMutation(ctx, watch.Event{Type: watch.Added, Object: u1}, ch); err != nil {
		t.Fatalf("processMutation(Add) error: %v", err)
	}

	if _, err := rm.processMutation(ctx, watch.Event{Type: watch.Modified, Object: u2}, ch); err != nil {
		t.Fatalf("processMutation(Modify) error: %v", err)
	}

	if _, err := rm.processMutation(ctx, watch.Event{Type: watch.Deleted, Object: u3}, ch); err != nil {
		t.Fatalf("processMutation(Delete) error: %v", err)
	}

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

func TestRemoveListSuffix(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedOutput string
	}{
		"empty string": {
			input:          "",
			expectedOutput: "",
		},
		"no List suffix": {
			input:          "Pod",
			expectedOutput: "Pod",
		},
		"with List suffix": {
			input:          "PodList",
			expectedOutput: "Pod",
		},
		"multiple capitalizations": {
			input:          "StatefulSetList",
			expectedOutput: "StatefulSet",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := removeListSuffix(tt.input)
			if result != tt.expectedOutput {
				t.Errorf("removeListSuffix(%q) = %q, want %q", tt.input, result, tt.expectedOutput)
			}
		})
	}
}
