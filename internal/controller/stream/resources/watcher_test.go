// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// mockResourceStreamSender is a mock implementation of ResourceStreamSender for testing.
type mockResourceStreamSender struct{}

func (m *mockResourceStreamSender) SendObjectData(_ *zap.Logger, _ *pb.KubernetesObjectData) error {
	return nil
}

func (m *mockResourceStreamSender) CreateMutationObject(metadata *pb.KubernetesObjectData, eventType watch.EventType) *pb.KubernetesResourceMutation {
	var mutation *pb.KubernetesResourceMutation

	switch eventType {
	case watch.Added:
		mutation = &pb.KubernetesResourceMutation{
			Mutation: &pb.KubernetesResourceMutation_CreateResource{
				CreateResource: metadata,
			},
		}
	case watch.Deleted:
		mutation = &pb.KubernetesResourceMutation{
			Mutation: &pb.KubernetesResourceMutation_DeleteResource{
				DeleteResource: metadata,
			},
		}
	case watch.Modified:
		mutation = &pb.KubernetesResourceMutation{
			Mutation: &pb.KubernetesResourceMutation_UpdateResource{
				UpdateResource: metadata,
			},
		}
	case watch.Bookmark:
	case watch.Error:
	}

	return mutation
}

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
		resourceName:    "namespaces",
		dynamicClient:   dyn,
		logger:          logger,
		resourcesClient: &mockResourceStreamSender{},
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
		resourceName:    "namespaces",
		dynamicClient:   dyn,
		logger:          logger,
		resourcesClient: &mockResourceStreamSender{},
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
		resourceName:    "namespaces",
		logger:          logger,
		resourcesClient: &mockResourceStreamSender{},
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
		resourceName:    "namespaces",
		logger:          logger,
		resourcesClient: &mockResourceStreamSender{},
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
		resourceName:    "namespaces",
		logger:          logger,
		resourcesClient: &mockResourceStreamSender{},
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

func TestProcessMutation_BookmarkSendsNilMutation(t *testing.T) {
	logger := zap.NewNop()
	rm := &Watcher{
		resourceName:    "namespaces",
		logger:          logger,
		resourcesClient: &mockResourceStreamSender{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan *pb.KubernetesResourceMutation, 1)

	u := newUnstructuredNamespace("n1", "99")

	rv, err := rm.processMutation(ctx, watch.Event{Type: watch.Bookmark, Object: u}, ch)
	if err != nil {
		t.Fatalf("processMutation(Bookmark) error: %v", err)
	}

	if rv != "99" {
		t.Errorf("expected resourceVersion '99', got %q", rv)
	}

	// Bookmark sends nil mutation to channel (CreateMutationObject returns nil for Bookmark)
	m := <-ch
	if m != nil {
		t.Fatalf("expected nil mutation for Bookmark, got %#v", m)
	}
}

func TestProcessMutation_ErrorEventSendsNilMutation(t *testing.T) {
	logger := zap.NewNop()
	rm := &Watcher{
		resourceName:    "namespaces",
		logger:          logger,
		resourcesClient: &mockResourceStreamSender{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan *pb.KubernetesResourceMutation, 1)

	u := newUnstructuredNamespace("n1", "10")
	_, err := rm.processMutation(ctx, watch.Event{Type: watch.Error, Object: u}, ch)

	// Error event doesn't return error, it sends nil mutation
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// CreateMutationObject returns nil for Error event type
	m := <-ch
	if m != nil {
		t.Fatalf("expected nil mutation for Error event, got %#v", m)
	}
}

func TestProcessMutation_NilObject(t *testing.T) {
	logger := zap.NewNop()
	rm := &Watcher{
		resourceName:    "namespaces",
		logger:          logger,
		resourcesClient: &mockResourceStreamSender{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan *pb.KubernetesResourceMutation, 1)

	_, err := rm.processMutation(ctx, watch.Event{Type: watch.Added, Object: nil}, ch)
	if err == nil {
		t.Fatalf("expected error for nil object, got nil")
	}
}

func TestNewWatcher_CreatesWatcher(t *testing.T) {
	logger := zap.NewNop()
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)

	fakeWatcher := watch.NewFake()
	dyn.PrependWatchReactor("*", func(action clientgotesting.Action) (handled bool, ret watch.Interface, err error) {
		return true, fakeWatcher, nil
	})

	rm := &Watcher{
		resourceName:    "pods",
		apiGroup:        "",
		dynamicClient:   dyn,
		logger:          logger,
		resourcesClient: &mockResourceStreamSender{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, err := rm.newWatcher(ctx, "1", logger)
	if err != nil {
		t.Fatalf("newWatcher error: %v", err)
	}

	if w == nil {
		t.Fatalf("expected non-nil watcher")
	}
}

func TestGetResourceVersionFromBookmark_ValidObject(t *testing.T) {
	u := newUnstructuredNamespace("test", "123")
	ev := watch.Event{Type: watch.Bookmark, Object: u}

	rv, err := getResourceVersionFromBookmark(ev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rv != "123" {
		t.Errorf("expected resourceVersion '123', got %q", rv)
	}
}

func TestGetResourceVersionFromBookmark_NilObject(t *testing.T) {
	ev := watch.Event{Type: watch.Bookmark, Object: nil}

	rv, err := getResourceVersionFromBookmark(ev)
	if err == nil {
		t.Fatalf("expected error for nil object, got nil")
	}

	if rv != "" {
		t.Errorf("expected empty resourceVersion, got %q", rv)
	}
}

func TestGetResourceVersionFromBookmark_EmptyResourceVersion(t *testing.T) {
	u := newUnstructuredNamespace("test", "")
	ev := watch.Event{Type: watch.Bookmark, Object: u}

	rv, err := getResourceVersionFromBookmark(ev)
	if err == nil {
		t.Fatalf("expected error for empty resourceVersion, got nil")
	}

	if rv != "" {
		t.Errorf("expected empty resourceVersion, got %q", rv)
	}
}

func TestGetErrFromWatchEvent_NilObject(t *testing.T) {
	ev := watch.Event{Type: watch.Error, Object: nil}

	err := getErrFromWatchEvent(ev)
	if err != nil {
		t.Errorf("expected nil error for nil object, got %v", err)
	}
}

func TestGetErrFromWatchEvent_NonErrorType(t *testing.T) {
	u := newUnstructuredNamespace("test", "123")
	ev := watch.Event{Type: watch.Added, Object: u}

	err := getErrFromWatchEvent(ev)
	if err != nil {
		t.Errorf("expected nil error for non-Error event type, got %v", err)
	}
}

func TestGetErrFromWatchEvent_WithStatus(t *testing.T) {
	status := &metav1.Status{
		Status:  metav1.StatusFailure,
		Message: "resource version expired",
		Reason:  metav1.StatusReasonExpired,
		Code:    410,
	}
	ev := watch.Event{Type: watch.Error, Object: status}

	err := getErrFromWatchEvent(ev)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	if !apierrors.IsResourceExpired(err) {
		t.Errorf("expected ResourceExpired error, got %v", err)
	}
}

func TestGetErrFromWatchEvent_UnexpectedObjectType(t *testing.T) {
	u := newUnstructuredNamespace("test", "123")
	ev := watch.Event{Type: watch.Error, Object: u}

	err := getErrFromWatchEvent(ev)
	if err == nil {
		t.Fatalf("expected error for unexpected object type, got nil")
	}

	if !strings.Contains(err.Error(), "unexpected error type") {
		t.Errorf("expected 'unexpected error type' error, got %v", err)
	}
}

func TestHandleWatchError_ExpiredResourceVersion(t *testing.T) {
	logger := zap.NewNop()
	rm := &Watcher{
		resourceName: "namespaces",
		logger:       logger,
	}

	expiredErr := apierrors.NewResourceExpired("resource version expired")

	shouldBreak, returnErr := rm.handleWatchError(expiredErr, "100", logger)

	if shouldBreak {
		t.Errorf("expected shouldBreak=false for expired error, got true")
	}

	if returnErr == nil {
		t.Fatalf("expected returnErr to be non-nil for expired error")
	}
}

func TestHandleWatchError_OtherError(t *testing.T) {
	logger := zap.NewNop()
	rm := &Watcher{
		resourceName: "namespaces",
		logger:       logger,
	}

	otherErr := errors.New("some other error")

	shouldBreak, returnErr := rm.handleWatchError(otherErr, "100", logger)

	if !shouldBreak {
		t.Errorf("expected shouldBreak=true for other errors, got false")
	}

	if returnErr != nil {
		t.Errorf("expected returnErr=nil for other errors, got %v", returnErr)
	}
}

func TestHandleWatchEvent_ErrorEvent(t *testing.T) {
	logger := zap.NewNop()
	rm := &Watcher{
		resourceName:    "namespaces",
		logger:          logger,
		resourcesClient: &mockResourceStreamSender{},
	}

	ctx := context.Background()
	mutationChan := make(chan *pb.KubernetesResourceMutation, 1)

	status := &metav1.Status{
		Status:  metav1.StatusFailure,
		Message: "test error",
		Reason:  metav1.StatusReasonInternalError,
		Code:    500,
	}
	ev := watch.Event{Type: watch.Error, Object: status}

	rv, isMutation, err := rm.handleWatchEvent(ctx, ev, mutationChan, logger)
	if err == nil {
		t.Fatalf("expected error for Error event, got nil")
	}

	if rv != "" {
		t.Errorf("expected empty resourceVersion, got %q", rv)
	}

	if isMutation {
		t.Errorf("expected isMutation=false for Error event")
	}
}

func TestHandleWatchEvent_BookmarkEvent(t *testing.T) {
	logger := zap.NewNop()
	rm := &Watcher{
		resourceName:    "namespaces",
		logger:          logger,
		resourcesClient: &mockResourceStreamSender{},
	}

	ctx := context.Background()
	mutationChan := make(chan *pb.KubernetesResourceMutation, 1)

	u := newUnstructuredNamespace("test", "456")
	ev := watch.Event{Type: watch.Bookmark, Object: u}

	rv, isMutation, err := rm.handleWatchEvent(ctx, ev, mutationChan, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rv != "456" {
		t.Errorf("expected resourceVersion '456', got %q", rv)
	}

	if isMutation {
		t.Errorf("expected isMutation=false for Bookmark event")
	}
}

func TestHandleWatchEvent_AddedEvent(t *testing.T) {
	logger := zap.NewNop()
	rm := &Watcher{
		resourceName:    "namespaces",
		logger:          logger,
		resourcesClient: &mockResourceStreamSender{},
	}

	ctx := context.Background()
	mutationChan := make(chan *pb.KubernetesResourceMutation, 1)

	u := newUnstructuredNamespace("test-ns", "789")
	ev := watch.Event{Type: watch.Added, Object: u}

	rv, isMutation, err := rm.handleWatchEvent(ctx, ev, mutationChan, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rv != "789" {
		t.Errorf("expected resourceVersion '789', got %q", rv)
	}

	if !isMutation {
		t.Errorf("expected isMutation=true for Added event")
	}

	// Verify mutation was sent
	select {
	case m := <-mutationChan:
		if m.GetCreateResource() == nil {
			t.Errorf("expected CreateResource mutation")
		}
	default:
		t.Fatalf("expected mutation in channel")
	}
}

func TestHandleWatchEvent_UnknownEventType(t *testing.T) {
	logger := zap.NewNop()
	rm := &Watcher{
		resourceName:    "namespaces",
		logger:          logger,
		resourcesClient: &mockResourceStreamSender{},
	}

	ctx := context.Background()
	mutationChan := make(chan *pb.KubernetesResourceMutation, 1)

	u := newUnstructuredNamespace("test", "123")
	ev := watch.Event{Type: watch.EventType("unknown"), Object: u}

	rv, isMutation, err := rm.handleWatchEvent(ctx, ev, mutationChan, logger)
	if err == nil {
		t.Fatalf("expected error for unknown event type, got nil")
	}

	if rv != "" {
		t.Errorf("expected empty resourceVersion, got %q", rv)
	}

	if isMutation {
		t.Errorf("expected isMutation=false for unknown event")
	}
}

func TestNewWatcher_Constructor(t *testing.T) {
	logger := zap.NewNop()
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)
	clientset := fake.NewClientset()
	limiter := rate.NewLimiter(1, 5)

	config := WatcherConfig{
		ResourceName:    "pods",
		ApiGroup:        "",
		Clientset:       clientset,
		BaseLogger:      logger,
		DynamicClient:   dyn,
		ResourcesClient: &mockResourceStreamSender{},
		Limiter:         limiter,
	}

	watcher := NewWatcher(config)

	if watcher == nil {
		t.Fatalf("expected non-nil watcher")
	}

	if watcher.resourceName != "pods" {
		t.Errorf("expected resourceName 'pods', got %q", watcher.resourceName)
	}

	if watcher.apiGroup != "" {
		t.Errorf("expected empty apiGroup, got %q", watcher.apiGroup)
	}

	if watcher.clientset != clientset {
		t.Errorf("clientset not set correctly")
	}

	if watcher.dynamicClient != dyn {
		t.Errorf("dynamicClient not set correctly")
	}

	if watcher.limiter != limiter {
		t.Errorf("limiter not set correctly")
	}
}

func TestFetchResources_Success(t *testing.T) {
	logger := zap.NewNop()
	scheme := runtime.NewScheme()

	pod := newUnstructuredPod("test-pod", "100")
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			{Group: "", Version: "v1", Resource: "pods"}: "PodList",
		},
		pod,
	)

	rm := &Watcher{
		resourceName:  "pods",
		apiGroup:      "",
		dynamicClient: dyn,
		logger:        logger,
	}

	ctx := context.Background()
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}

	result, err := rm.FetchResources(ctx, gvr, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatalf("expected non-nil result")
	}

	if len(result.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(result.Items))
	}
}

func TestFetchResources_Forbidden(t *testing.T) {
	logger := zap.NewNop()
	scheme := runtime.NewScheme()
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}

	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			gvr: "PodList",
		},
	)

	forbiddenErr := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("forbidden"))

	dyn.PrependReactor("list", "*", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, nil, forbiddenErr
	})

	rm := &Watcher{
		resourceName:  "pods",
		apiGroup:      "",
		dynamicClient: dyn,
		logger:        logger,
	}

	ctx := context.Background()

	_, err := rm.FetchResources(ctx, gvr, "default")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	if !apierrors.IsForbidden(err) {
		t.Errorf("expected Forbidden error, got %v", err)
	}
}

func TestExtractObjectMetas_Success(t *testing.T) {
	logger := zap.NewNop()

	rm := &Watcher{
		logger: logger,
	}

	pod1 := newUnstructuredPod("pod1", "100")
	pod2 := newUnstructuredPod("pod2", "101")

	list := &unstructured.UnstructuredList{
		Items: []unstructured.Unstructured{*pod1, *pod2},
	}

	metas, err := rm.ExtractObjectMetas(list)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(metas) != 2 {
		t.Errorf("expected 2 metas, got %d", len(metas))
	}

	if metas[0].Name != "pod1" {
		t.Errorf("expected name 'pod1', got %q", metas[0].Name)
	}

	if metas[1].Name != "pod2" {
		t.Errorf("expected name 'pod2', got %q", metas[1].Name)
	}
}

func TestExtractObjectMetas_EmptyList(t *testing.T) {
	logger := zap.NewNop()

	rm := &Watcher{
		logger: logger,
	}

	list := &unstructured.UnstructuredList{
		Items: []unstructured.Unstructured{},
	}

	metas, err := rm.ExtractObjectMetas(list)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(metas) != 0 {
		t.Errorf("expected 0 metas, got %d", len(metas))
	}
}

func TestListResources_Success(t *testing.T) {
	logger := zap.NewNop()
	scheme := runtime.NewScheme()

	pod := newUnstructuredPod("test-pod", "100")
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}

	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			gvr: "PodList",
		},
		pod,
	)

	rm := &Watcher{
		resourceName:  "pods",
		apiGroup:      "",
		dynamicClient: dyn,
		logger:        logger,
	}

	ctx := context.Background()

	metas, _, kind, err := rm.ListResources(ctx, gvr, metav1.NamespaceAll)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(metas) != 1 {
		t.Errorf("expected 1 meta, got %d", len(metas))
	}

	// Note: fake dynamic client may not set listVersion, so we don't check it

	if kind != "Pod" {
		t.Errorf("expected kind 'Pod', got %q", kind)
	}
}

// helper: minimal unstructured Pod with name and rv in "default" namespace
func newUnstructuredPod(name, rv string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("v1")
	obj.SetKind("Pod")
	obj.SetName(name)
	obj.SetNamespace("default")
	obj.SetResourceVersion(rv)

	return obj
}
