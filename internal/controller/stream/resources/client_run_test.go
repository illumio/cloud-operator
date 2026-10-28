// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/k8sclient"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// fakeK8sClient serves the clientset and dynamic client Run uses. Other
// k8sclient.Client methods are not called by Run and panic if they are.
type fakeK8sClient struct {
	k8sclient.Client

	clientset kubernetes.Interface
	dynamic   dynamic.Interface
}

func (f *fakeK8sClient) GetClientset() kubernetes.Interface { return f.clientset }

func (f *fakeK8sClient) GetDynamicClient() dynamic.Interface { return f.dynamic }

// recordingResourcesStream records sent requests and signals when the snapshot
// is complete.
type recordingResourcesStream struct {
	mutex            sync.Mutex
	sent             []*pb.SendKubernetesResourcesRequest
	snapshotComplete chan struct{}
}

func (s *recordingResourcesStream) Send(req *pb.SendKubernetesResourcesRequest) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.sent = append(s.sent, req)

	// The snapshot-complete request carries an empty oneof wrapper, so its getter
	// returns nil: match on the wrapper type.
	if _, ok := req.GetRequest().(*pb.SendKubernetesResourcesRequest_ResourceSnapshotComplete); ok {
		close(s.snapshotComplete)
	}

	return nil
}

func (s *recordingResourcesStream) Recv() (*pb.SendKubernetesResourcesResponse, error) {
	return nil, errors.New("not implemented")
}

func (s *recordingResourcesStream) resourceData() []*pb.KubernetesObjectData {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	var objects []*pb.KubernetesObjectData

	for _, req := range s.sent {
		if data := req.GetResourceData(); data != nil {
			objects = append(objects, data)
		}
	}

	return objects
}

func newGatewayAPIObject(apiVersion, kind, name string, spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": "default", "resourceVersion": "1"},
		"spec":       spec,
	}}
}

// An operator upgraded without its Helm chart has no RBAC for the new route
// kinds. Listing them is Forbidden: the resource is skipped and the rest of the
// snapshot, including the new Gateway API data, is still sent.
func TestRun_SkipsForbiddenRouteKinds(t *testing.T) {
	clientset := k8sfake.NewSimpleClientset(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "kube-system", UID: "cluster-uid"},
	})

	fakeDiscovery, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
	require.True(t, ok, "failed to get fake discovery client")

	fakeDiscovery.Resources = []*metav1.APIResourceList{
		{GroupVersion: "gateway.networking.k8s.io/v1", APIResources: []metav1.APIResource{
			{Name: "gateways", Kind: "Gateway", Namespaced: true},
		}},
		{GroupVersion: "gateway.networking.k8s.io/v1alpha2", APIResources: []metav1.APIResource{
			{Name: "tcproutes", Kind: "TCPRoute", Namespaced: true},
			{Name: "udproutes", Kind: "UDPRoute", Namespaced: true},
		}},
	}

	parentRefs := map[string]any{"parentRefs": []any{map[string]any{"name": "edge"}}}

	gatewaysGVR := schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}
	tcpRoutesGVR := schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1alpha2", Resource: "tcproutes"}
	udpRoutesGVR := schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1alpha2", Resource: "udproutes"}

	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			gatewaysGVR:  "GatewayList",
			tcpRoutesGVR: "TCPRouteList",
			udpRoutesGVR: "UDPRouteList",
		},
	)

	// Objects are added with an explicit resource: the fake client would guess
	// "gatewaies" from the Gateway kind.
	for gvr, obj := range map[schema.GroupVersionResource]*unstructured.Unstructured{
		gatewaysGVR:  newGatewayAPIObject("gateway.networking.k8s.io/v1", "Gateway", "edge", map[string]any{"gatewayClassName": "eg"}),
		tcpRoutesGVR: newGatewayAPIObject("gateway.networking.k8s.io/v1alpha2", "TCPRoute", "tcp", parentRefs),
		udpRoutesGVR: newGatewayAPIObject("gateway.networking.k8s.io/v1alpha2", "UDPRoute", "udp", parentRefs),
	} {
		require.NoError(t, dynamicClient.Tracker().Create(gvr, obj, "default"))
	}

	dynamicClient.PrependReactor("list", "tcproutes", func(clientgotesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Group: "gateway.networking.k8s.io", Resource: "tcproutes"}, "", errors.New("no RBAC"))
	})

	grpcStream := &recordingResourcesStream{snapshotComplete: make(chan struct{})}
	client := &resourcesClient{
		grpcStream: grpcStream,
		logger:     zap.NewNop(),
		k8sClient:  &fakeK8sClient{clientset: clientset, dynamic: dynamicClient},
		stats:      stream.NewStats(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErr := make(chan error, 1)

	go func() { runErr <- client.Run(ctx) }()

	select {
	case <-grpcStream.snapshotComplete:
	case err := <-runErr:
		t.Fatalf("Run returned before the snapshot was complete: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the snapshot")
	}

	cancel()
	require.ErrorIs(t, <-runErr, context.Canceled)

	sent := make(map[string]*pb.KubernetesObjectData)
	for _, object := range grpcStream.resourceData() {
		sent[object.GetKind()] = object
	}

	assert.NotContains(t, sent, "TCPRoute", "the Forbidden resource is skipped")
	require.Contains(t, sent, "Gateway")
	require.Contains(t, sent, "UDPRoute")
	assert.Equal(t, "eg", sent["Gateway"].GetGateway().GetGatewayClassName())
	assert.Equal(t, "edge", sent["UDPRoute"].GetGatewayRoute().GetParentRefs()[0].GetName())
}
