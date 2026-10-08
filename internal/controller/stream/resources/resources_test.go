// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/convert/anp"
	"github.com/illumio/cloud-operator/internal/convert/awsvpccni"
	"github.com/illumio/cloud-operator/internal/convert/cilium"
	"github.com/illumio/cloud-operator/internal/convert/ovn"
)

// gr builds a schema.GroupResource for test tables.
func gr(group, resource string) schema.GroupResource {
	return schema.GroupResource{Group: group, Resource: resource}
}

func TestBuildResourceApiGroupMap(t *testing.T) {
	logger := zap.NewNop()

	t.Run("maps resources to API groups", func(t *testing.T) {
		clientset := k8sfake.NewSimpleClientset()

		fakeDiscovery, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
		require.True(t, ok, "failed to get fake discovery client")

		fakeDiscovery.Resources = []*metav1.APIResourceList{
			{
				GroupVersion: "v1",
				APIResources: []metav1.APIResource{
					{Name: "pods", Kind: "Pod"},
					{Name: "services", Kind: "Service"},
				},
			},
			{
				GroupVersion: "apps/v1",
				APIResources: []metav1.APIResource{
					{Name: "deployments", Kind: "Deployment"},
				},
			},
		}

		resources := []schema.GroupResource{gr("", "pods"), gr("apps", "deployments")}

		result, err := BuildResourceAPIGroupMap(resources, clientset, logger)
		require.NoError(t, err)

		// pods should be in core group (empty string)
		resourceInfo, ok := result[gr("", "pods")]
		assert.True(t, ok, "expected 'pods' to be in result")
		assert.Empty(t, resourceInfo.Group, "expected pods apiGroup to be empty")
		assert.Equal(t, "v1", resourceInfo.Version)
	})

	t.Run("handles empty resources", func(t *testing.T) {
		clientset := k8sfake.NewSimpleClientset()

		resources := []schema.GroupResource{}

		result, err := BuildResourceAPIGroupMap(resources, clientset, logger)
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("skips metrics.k8s.io group", func(t *testing.T) {
		clientset := k8sfake.NewSimpleClientset()

		fakeDiscovery, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
		require.True(t, ok, "failed to get fake discovery client")

		fakeDiscovery.Resources = []*metav1.APIResourceList{
			{
				GroupVersion: "metrics.k8s.io/v1beta1",
				APIResources: []metav1.APIResource{
					{Name: "nodes", Kind: "NodeMetrics"},
				},
			},
		}

		resources := []schema.GroupResource{gr("metrics.k8s.io", "nodes")}

		result, err := BuildResourceAPIGroupMap(resources, clientset, logger)
		require.NoError(t, err)

		// nodes should NOT be mapped because metrics.k8s.io is skipped
		_, ok = result[gr("metrics.k8s.io", "nodes")]
		assert.False(t, ok, "expected 'nodes' to be skipped for metrics.k8s.io group")
	})

	t.Run("requested resource not in any discovered group", func(t *testing.T) {
		clientset := k8sfake.NewSimpleClientset()

		fakeDiscovery, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
		require.True(t, ok)

		fakeDiscovery.Resources = []*metav1.APIResourceList{
			{
				GroupVersion: "v1",
				APIResources: []metav1.APIResource{
					{Name: "pods", Kind: "Pod"},
				},
			},
		}

		result, err := BuildResourceAPIGroupMap([]schema.GroupResource{gr("apps", "deployments")}, clientset, logger)
		require.NoError(t, err)
		assert.Empty(t, result, "expected no match for resource not present in any group")
	})

	t.Run("handles apps group resources", func(t *testing.T) {
		clientset := k8sfake.NewSimpleClientset()

		fakeDiscovery, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
		require.True(t, ok, "failed to get fake discovery client")

		fakeDiscovery.Resources = []*metav1.APIResourceList{
			{
				GroupVersion: "apps/v1",
				APIResources: []metav1.APIResource{
					{Name: "deployments", Kind: "Deployment"},
					{Name: "statefulsets", Kind: "StatefulSet"},
					{Name: "daemonsets", Kind: "DaemonSet"},
				},
			},
		}

		resources := []schema.GroupResource{gr("apps", "deployments"), gr("apps", "statefulsets")}

		result, err := BuildResourceAPIGroupMap(resources, clientset, logger)
		require.NoError(t, err)

		assert.Equal(t, map[schema.GroupResource]ResourceInfo{
			gr("apps", "deployments"):  {Group: "apps", Version: "v1"},
			gr("apps", "statefulsets"): {Group: "apps", Version: "v1"},
		}, result)
	})

	// Several CRD groups serve the same plural names as the resources we watch.
	// Only the requested group is mapped.
	t.Run("same-named resources in other groups are not mapped", func(t *testing.T) {
		clientset := k8sfake.NewSimpleClientset()

		fakeDiscovery, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
		require.True(t, ok, "failed to get fake discovery client")

		fakeDiscovery.Resources = []*metav1.APIResourceList{
			{GroupVersion: "v1", APIResources: []metav1.APIResource{
				{Name: "nodes", Kind: "Node"},
				{Name: "services", Kind: "Service"},
			}},
			{GroupVersion: "networking.k8s.io/v1", APIResources: []metav1.APIResource{
				{Name: "ingresses", Kind: "Ingress"},
				{Name: "networkpolicies", Kind: "NetworkPolicy"},
			}},
			{GroupVersion: "gateway.networking.k8s.io/v1", APIResources: []metav1.APIResource{
				{Name: "gateways", Kind: "Gateway"},
			}},
			{GroupVersion: "networking.k8s.aws/v1alpha1", APIResources: []metav1.APIResource{
				{Name: "clusternetworkpolicies", Kind: "ClusterNetworkPolicy"},
			}},
			{GroupVersion: "config.openshift.io/v1", APIResources: []metav1.APIResource{
				{Name: "nodes", Kind: "Node"},
				{Name: "ingresses", Kind: "Ingress"},
			}},
			{GroupVersion: "serving.knative.dev/v1", APIResources: []metav1.APIResource{
				{Name: "services", Kind: "Service"},
			}},
			{GroupVersion: "crd.projectcalico.org/v1", APIResources: []metav1.APIResource{
				{Name: "networkpolicies", Kind: "NetworkPolicy"},
			}},
			{GroupVersion: "networking.istio.io/v1", APIResources: []metav1.APIResource{
				{Name: "gateways", Kind: "Gateway"},
			}},
			{GroupVersion: "policy.networking.k8s.io/v1alpha2", APIResources: []metav1.APIResource{
				{Name: "clusternetworkpolicies", Kind: "ClusterNetworkPolicy"},
			}},
		}

		resources := []schema.GroupResource{
			gr("", "nodes"),
			gr("", "services"),
			gr("networking.k8s.io", "ingresses"),
			gr("networking.k8s.io", "networkpolicies"),
			gr("gateway.networking.k8s.io", "gateways"),
			gr("networking.k8s.aws", "clusternetworkpolicies"),
		}

		result, err := BuildResourceAPIGroupMap(resources, clientset, logger)
		require.NoError(t, err)

		assert.Equal(t, map[schema.GroupResource]ResourceInfo{
			gr("", "nodes"):                                    {Group: "", Version: "v1"},
			gr("", "services"):                                 {Group: "", Version: "v1"},
			gr("networking.k8s.io", "ingresses"):               {Group: "networking.k8s.io", Version: "v1"},
			gr("networking.k8s.io", "networkpolicies"):         {Group: "networking.k8s.io", Version: "v1"},
			gr("gateway.networking.k8s.io", "gateways"):        {Group: "gateway.networking.k8s.io", Version: "v1"},
			gr("networking.k8s.aws", "clusternetworkpolicies"): {Group: "networking.k8s.aws", Version: "v1alpha1"},
		}, result)
	})

	// Resources that share a plural name in different groups can be watched
	// together: both Gateway API and Istio gateways are mapped when requested.
	t.Run("same plural name in two requested groups maps both", func(t *testing.T) {
		clientset := k8sfake.NewSimpleClientset()

		fakeDiscovery, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
		require.True(t, ok, "failed to get fake discovery client")

		fakeDiscovery.Resources = []*metav1.APIResourceList{
			{GroupVersion: "gateway.networking.k8s.io/v1", APIResources: []metav1.APIResource{
				{Name: "gateways", Kind: "Gateway"},
			}},
			{GroupVersion: "networking.istio.io/v1", APIResources: []metav1.APIResource{
				{Name: "gateways", Kind: "Gateway"},
			}},
		}

		resources := []schema.GroupResource{
			gr("gateway.networking.k8s.io", "gateways"),
			gr("networking.istio.io", "gateways"),
		}

		result, err := buildResourceAPIGroupMap(resources, clientset, logger, true)
		require.NoError(t, err)

		assert.Equal(t, map[schema.GroupResource]ResourceInfo{
			gr("gateway.networking.k8s.io", "gateways"): {Group: "gateway.networking.k8s.io", Version: "v1"},
			gr("networking.istio.io", "gateways"):       {Group: "networking.istio.io", Version: "v1"},
		}, result)
	})

	t.Run("resource absent from its group is not taken from another group", func(t *testing.T) {
		clientset := k8sfake.NewSimpleClientset()

		fakeDiscovery, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
		require.True(t, ok, "failed to get fake discovery client")

		fakeDiscovery.Resources = []*metav1.APIResourceList{
			{GroupVersion: "networking.istio.io/v1", APIResources: []metav1.APIResource{
				{Name: "gateways", Kind: "Gateway"},
			}},
		}

		result, err := buildResourceAPIGroupMap([]schema.GroupResource{gr("gateway.networking.k8s.io", "gateways")}, clientset, logger, true)
		require.NoError(t, err)
		assert.Empty(t, result)
	})
}

// The fake discovery client treats the first listed version of a group as the
// preferred version, so these fixtures list the preferred version first.
func TestBuildResourceAPIGroupMap_VersionFallback(t *testing.T) {
	logger := zap.NewNop()

	newClientset := func(t *testing.T) *k8sfake.Clientset {
		t.Helper()

		clientset := k8sfake.NewSimpleClientset()

		fakeDiscovery, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
		require.True(t, ok, "failed to get fake discovery client")

		fakeDiscovery.Resources = []*metav1.APIResourceList{
			// MetalLB prefers v1beta2 (BGPPeer) but serves the status CRDs only in v1beta1.
			{GroupVersion: "metallb.io/v1beta2", APIResources: []metav1.APIResource{
				{Name: "bgppeers", Kind: "BGPPeer"},
			}},
			{GroupVersion: "metallb.io/v1beta1", APIResources: []metav1.APIResource{
				{Name: "bgppeers", Kind: "BGPPeer"},
				{Name: "servicel2statuses", Kind: "ServiceL2Status"},
				{Name: "servicebgpstatuses", Kind: "ServiceBGPStatus"},
			}},
			// GKE can prefer networking.gke.io/v1 while svcneg stays in v1beta1.
			{GroupVersion: "networking.gke.io/v1", APIResources: []metav1.APIResource{
				{Name: "managedcertificates", Kind: "ManagedCertificate"},
			}},
			{GroupVersion: "networking.gke.io/v1beta1", APIResources: []metav1.APIResource{
				{Name: "servicenetworkendpointgroups", Kind: "ServiceNetworkEndpointGroup"},
			}},
			// A resource served in both versions resolves to the preferred one.
			{GroupVersion: "elbv2.k8s.aws/v1beta1", APIResources: []metav1.APIResource{
				{Name: "targetgroupbindings", Kind: "TargetGroupBinding"},
			}},
			{GroupVersion: "elbv2.k8s.aws/v1alpha1", APIResources: []metav1.APIResource{
				{Name: "targetgroupbindings", Kind: "TargetGroupBinding"},
			}},
		}

		return clientset
	}

	resources := []schema.GroupResource{
		gr("metallb.io", "servicel2statuses"),
		gr("metallb.io", "servicebgpstatuses"),
		gr("networking.gke.io", "servicenetworkendpointgroups"),
		gr("elbv2.k8s.aws", "targetgroupbindings"),
	}

	t.Run("resource stream searches every served version", func(t *testing.T) {
		result, err := buildResourceAPIGroupMap(resources, newClientset(t), logger, true)
		require.NoError(t, err)

		assert.Equal(t, map[schema.GroupResource]ResourceInfo{
			gr("metallb.io", "servicel2statuses"):                   {Group: "metallb.io", Version: "v1beta1"},
			gr("metallb.io", "servicebgpstatuses"):                  {Group: "metallb.io", Version: "v1beta1"},
			gr("networking.gke.io", "servicenetworkendpointgroups"): {Group: "networking.gke.io", Version: "v1beta1"},
			gr("elbv2.k8s.aws", "targetgroupbindings"):              {Group: "elbv2.k8s.aws", Version: "v1beta1"},
		}, result)
	})

	// The reconciler applies objects at the version it discovers, so it keeps
	// using only the preferred version.
	t.Run("exported map only uses preferred versions", func(t *testing.T) {
		result, err := BuildResourceAPIGroupMap(resources, newClientset(t), logger)
		require.NoError(t, err)

		assert.Equal(t, map[schema.GroupResource]ResourceInfo{
			gr("elbv2.k8s.aws", "targetgroupbindings"): {Group: "elbv2.k8s.aws", Version: "v1beta1"},
		}, result)
	})
}

// Gateway API prefers v1, but TCPRoute, UDPRoute and TLSRoute are served only in
// alpha versions, and GRPCRoute only in v1alpha2 before Gateway API v1.1. Each
// route is watched at the highest-priority version that serves it.
func TestBuildResourceAPIGroupMap_GatewayAPIAlphaRoutes(t *testing.T) {
	clientset := k8sfake.NewSimpleClientset()

	fakeDiscovery, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
	require.True(t, ok, "failed to get fake discovery client")

	fakeDiscovery.Resources = []*metav1.APIResourceList{
		{GroupVersion: "gateway.networking.k8s.io/v1", APIResources: []metav1.APIResource{
			{Name: "gatewayclasses", Kind: "GatewayClass"},
			{Name: "gateways", Kind: "Gateway"},
			{Name: "httproutes", Kind: "HTTPRoute"},
		}},
		{GroupVersion: "gateway.networking.k8s.io/v1beta1", APIResources: []metav1.APIResource{
			{Name: "gateways", Kind: "Gateway"},
			{Name: "httproutes", Kind: "HTTPRoute"},
		}},
		{GroupVersion: "gateway.networking.k8s.io/v1alpha3", APIResources: []metav1.APIResource{
			{Name: "tlsroutes", Kind: "TLSRoute"},
		}},
		{GroupVersion: "gateway.networking.k8s.io/v1alpha2", APIResources: []metav1.APIResource{
			{Name: "grpcroutes", Kind: "GRPCRoute"},
			{Name: "tcproutes", Kind: "TCPRoute"},
			{Name: "tlsroutes", Kind: "TLSRoute"},
			{Name: "udproutes", Kind: "UDPRoute"},
		}},
	}

	gatewayResources := slices.DeleteFunc(slices.Clone(resourceList), func(groupResource schema.GroupResource) bool {
		return groupResource.Group != "gateway.networking.k8s.io"
	})

	result, err := buildResourceAPIGroupMap(gatewayResources, clientset, zap.NewNop(), true)
	require.NoError(t, err)

	assert.Equal(t, map[schema.GroupResource]ResourceInfo{
		gr("gateway.networking.k8s.io", "gatewayclasses"): {Group: "gateway.networking.k8s.io", Version: "v1"},
		gr("gateway.networking.k8s.io", "gateways"):       {Group: "gateway.networking.k8s.io", Version: "v1"},
		gr("gateway.networking.k8s.io", "httproutes"):     {Group: "gateway.networking.k8s.io", Version: "v1"},
		gr("gateway.networking.k8s.io", "grpcroutes"):     {Group: "gateway.networking.k8s.io", Version: "v1alpha2"},
		gr("gateway.networking.k8s.io", "tcproutes"):      {Group: "gateway.networking.k8s.io", Version: "v1alpha2"},
		gr("gateway.networking.k8s.io", "tlsroutes"):      {Group: "gateway.networking.k8s.io", Version: "v1alpha3"},
		gr("gateway.networking.k8s.io", "udproutes"):      {Group: "gateway.networking.k8s.io", Version: "v1alpha2"},
	}, result)
}

// Older or standard-channel Gateway API installs serve a subset of the route
// kinds, or none at v1. Only the served resources are watched, at the version
// the cluster serves them, and the missing ones are not an error.
func TestBuildResourceAPIGroupMap_GatewayAPIOlderInstalls(t *testing.T) {
	tests := map[string]struct {
		resources []*metav1.APIResourceList
		expected  map[schema.GroupResource]ResourceInfo
	}{
		"standard channel only: no alpha routes": {
			resources: []*metav1.APIResourceList{
				{GroupVersion: "gateway.networking.k8s.io/v1", APIResources: []metav1.APIResource{
					{Name: "gatewayclasses", Kind: "GatewayClass"},
					{Name: "gateways", Kind: "Gateway"},
					{Name: "grpcroutes", Kind: "GRPCRoute"},
					{Name: "httproutes", Kind: "HTTPRoute"},
				}},
			},
			expected: map[schema.GroupResource]ResourceInfo{
				gr("gateway.networking.k8s.io", "gatewayclasses"): {Group: "gateway.networking.k8s.io", Version: "v1"},
				gr("gateway.networking.k8s.io", "gateways"):       {Group: "gateway.networking.k8s.io", Version: "v1"},
				gr("gateway.networking.k8s.io", "grpcroutes"):     {Group: "gateway.networking.k8s.io", Version: "v1"},
				gr("gateway.networking.k8s.io", "httproutes"):     {Group: "gateway.networking.k8s.io", Version: "v1"},
			},
		},
		"pre-v1.0 install: v1beta1 preferred, GRPCRoute in v1alpha2": {
			resources: []*metav1.APIResourceList{
				{GroupVersion: "gateway.networking.k8s.io/v1beta1", APIResources: []metav1.APIResource{
					{Name: "gatewayclasses", Kind: "GatewayClass"},
					{Name: "gateways", Kind: "Gateway"},
					{Name: "httproutes", Kind: "HTTPRoute"},
				}},
				{GroupVersion: "gateway.networking.k8s.io/v1alpha2", APIResources: []metav1.APIResource{
					{Name: "gatewayclasses", Kind: "GatewayClass"},
					{Name: "gateways", Kind: "Gateway"},
					{Name: "grpcroutes", Kind: "GRPCRoute"},
					{Name: "httproutes", Kind: "HTTPRoute"},
				}},
			},
			expected: map[schema.GroupResource]ResourceInfo{
				gr("gateway.networking.k8s.io", "gatewayclasses"): {Group: "gateway.networking.k8s.io", Version: "v1beta1"},
				gr("gateway.networking.k8s.io", "gateways"):       {Group: "gateway.networking.k8s.io", Version: "v1beta1"},
				gr("gateway.networking.k8s.io", "httproutes"):     {Group: "gateway.networking.k8s.io", Version: "v1beta1"},
				gr("gateway.networking.k8s.io", "grpcroutes"):     {Group: "gateway.networking.k8s.io", Version: "v1alpha2"},
			},
		},
	}

	gatewayResources := slices.DeleteFunc(slices.Clone(resourceList), func(groupResource schema.GroupResource) bool {
		return groupResource.Group != "gateway.networking.k8s.io"
	})

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clientset := k8sfake.NewSimpleClientset()

			fakeDiscovery, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
			require.True(t, ok, "failed to get fake discovery client")

			fakeDiscovery.Resources = tt.resources

			result, err := buildResourceAPIGroupMap(gatewayResources, clientset, zap.NewNop(), true)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestResourceListHasNoDuplicates(t *testing.T) {
	seen := make(map[schema.GroupResource]struct{}, len(resourceList))

	for _, groupResource := range resourceList {
		_, duplicate := seen[groupResource]
		assert.False(t, duplicate, "resource %s is listed twice in resourceList", groupResource)

		seen[groupResource] = struct{}{}
	}
}

func TestManagedResourcesAreWatched(t *testing.T) {
	for _, groupResource := range ManagedResources {
		assert.True(t, slices.Contains(resourceList, groupResource), "managed resource %s must be in resourceList", groupResource)
	}
}

func TestBuildResourceAPIGroupMap_PolicyGroupCollisions(t *testing.T) {
	policyResources := []metav1.APIResource{
		{Name: "adminnetworkpolicies", Kind: "AdminNetworkPolicy"},
		{Name: "baselineadminnetworkpolicies", Kind: "BaselineAdminNetworkPolicy"},
	}
	egressResources := []metav1.APIResource{
		{Name: "egressfirewalls", Kind: "EgressFirewall"},
		{Name: "egressips", Kind: "EgressIP"},
	}
	awsResources := []metav1.APIResource{
		{Name: "clusternetworkpolicies", Kind: "ClusterNetworkPolicy"},
		{Name: "applicationnetworkpolicies", Kind: "ApplicationNetworkPolicy"},
	}
	ciliumResources := []metav1.APIResource{
		{Name: "ciliumnetworkpolicies", Kind: "CiliumNetworkPolicy"},
		{Name: "ciliumclusterwidenetworkpolicies", Kind: "CiliumClusterwideNetworkPolicy"},
		{Name: "ciliumcidrgroups", Kind: "CiliumCIDRGroup"},
	}
	supported := []*metav1.APIResourceList{
		{GroupVersion: "policy.networking.k8s.io/v1alpha1", APIResources: policyResources},
		{GroupVersion: "k8s.ovn.org/v1", APIResources: egressResources},
		{GroupVersion: "networking.k8s.aws/v1alpha1", APIResources: awsResources},
		{GroupVersion: "cilium.io/v2", APIResources: ciliumResources},
	}
	foreign := &metav1.APIResourceList{
		GroupVersion: "example.com/v1",
		APIResources: slices.Concat(policyResources, egressResources, awsResources, ciliumResources),
	}
	kubernetesCNP := &metav1.APIResourceList{
		GroupVersion: "policy.networking.k8s.io/v1alpha2",
		APIResources: []metav1.APIResource{{Name: "clusternetworkpolicies", Kind: "ClusterNetworkPolicy"}},
	}
	wantSupported := map[schema.GroupResource]ResourceInfo{
		gr("policy.networking.k8s.io", "adminnetworkpolicies"):         {Group: "policy.networking.k8s.io", Version: "v1alpha1"},
		gr("policy.networking.k8s.io", "baselineadminnetworkpolicies"): {Group: "policy.networking.k8s.io", Version: "v1alpha1"},
		gr("k8s.ovn.org", "egressfirewalls"):                           {Group: "k8s.ovn.org", Version: "v1"},
		gr("k8s.ovn.org", "egressips"):                                 {Group: "k8s.ovn.org", Version: "v1"},
		gr("networking.k8s.aws", "clusternetworkpolicies"):             {Group: "networking.k8s.aws", Version: "v1alpha1"},
		gr("networking.k8s.aws", "applicationnetworkpolicies"):         {Group: "networking.k8s.aws", Version: "v1alpha1"},
		gr("cilium.io", "ciliumnetworkpolicies"):                       {Group: "cilium.io", Version: "v2"},
		gr("cilium.io", "ciliumclusterwidenetworkpolicies"):            {Group: "cilium.io", Version: "v2"},
		gr("cilium.io", "ciliumcidrgroups"):                            {Group: "cilium.io", Version: "v2"},
	}

	tests := []struct {
		name      string
		resources []*metav1.APIResourceList
		want      map[schema.GroupResource]ResourceInfo
	}{
		{name: "supported groups", resources: supported, want: wantSupported},
		{name: "foreign group only", resources: []*metav1.APIResourceList{foreign}, want: map[schema.GroupResource]ResourceInfo{}},
		{name: "foreign group listed first", resources: append([]*metav1.APIResourceList{foreign}, supported...), want: wantSupported},
		{name: "foreign group listed last", resources: append(slices.Clone(supported), foreign), want: wantSupported},
		{name: "Kubernetes CNP is not an AWS resource", resources: []*metav1.APIResourceList{kubernetesCNP}, want: map[schema.GroupResource]ResourceInfo{}},
		{
			name: "AWS and Kubernetes CNP coexist",
			resources: []*metav1.APIResourceList{
				{GroupVersion: "networking.k8s.aws/v1alpha1", APIResources: awsResources},
				kubernetesCNP,
			},
			want: map[schema.GroupResource]ResourceInfo{
				gr("networking.k8s.aws", "clusternetworkpolicies"):     {Group: "networking.k8s.aws", Version: "v1alpha1"},
				gr("networking.k8s.aws", "applicationnetworkpolicies"): {Group: "networking.k8s.aws", Version: "v1alpha1"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientset := k8sfake.NewSimpleClientset()
			fakeDiscovery, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
			require.True(t, ok)

			fakeDiscovery.Resources = tt.resources

			got, err := BuildResourceAPIGroupMap(resourceList, clientset, zap.NewNop())
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// The dispatch tests check that each converter claims exactly its own entries of
// resourceList, using each entry's real API group.

func TestResourceListCiliumDispatchConsistency(t *testing.T) {
	expectedCilium := map[schema.GroupResource]bool{
		gr(cilium.APIGroup, "ciliumcidrgroups"):                 true,
		gr(cilium.APIGroup, "ciliumclusterwidenetworkpolicies"): true,
		gr(cilium.APIGroup, "ciliumnetworkpolicies"):            true,
	}

	for _, groupResource := range resourceList {
		isCilium := cilium.IsCiliumResource(groupResource.Group, groupResource.Resource)
		assert.Equal(t, expectedCilium[groupResource], isCilium, "IsCiliumResource(%s)", groupResource)
	}

	for groupResource := range expectedCilium {
		assert.True(t, slices.Contains(resourceList, groupResource), "expected Cilium resource %s must be in resourceList", groupResource)
	}
}

func TestResourceListAWSDispatchConsistency(t *testing.T) {
	// Both AWS policy resources are watched (ingested) and routed to the AWS converter.
	expectedAWS := map[schema.GroupResource]bool{
		gr(awsvpccni.APIGroup, "clusternetworkpolicies"):     true,
		gr(awsvpccni.APIGroup, "applicationnetworkpolicies"): true,
	}

	for _, groupResource := range resourceList {
		isAWS := awsvpccni.IsAWSResource(groupResource.Group, groupResource.Resource)
		assert.Equal(t, expectedAWS[groupResource], isAWS, "IsAWSResource(%s)", groupResource)
	}

	for groupResource := range expectedAWS {
		assert.True(t, slices.Contains(resourceList, groupResource), "%s must be in resourceList (ingested)", groupResource)
	}

	// ClusterNetworkPolicy is enforced/reconciled, so it must be in ManagedResources.
	assert.True(t, slices.Contains(ManagedResources, gr(awsvpccni.APIGroup, "clusternetworkpolicies")),
		"clusternetworkpolicies must be in ManagedResources (enforced)")

	// ApplicationNetworkPolicy is ingest-only (never enforced), so it must NOT be in
	// ManagedResources, otherwise the reconciler would try to apply/delete it.
	assert.False(t, slices.Contains(ManagedResources, gr(awsvpccni.APIGroup, "applicationnetworkpolicies")),
		"applicationnetworkpolicies must NOT be in ManagedResources (ingest-only)")
}

func TestResourceListAdminNetworkPolicyDispatchConsistency(t *testing.T) {
	expectedANP := map[schema.GroupResource]bool{
		gr(anp.APIGroup, "adminnetworkpolicies"):         true,
		gr(anp.APIGroup, "baselineadminnetworkpolicies"): true,
	}

	for _, groupResource := range resourceList {
		isANP := anp.IsAdminNetworkPolicyResource(groupResource.Group, groupResource.Resource)
		assert.Equal(t, expectedANP[groupResource], isANP, "IsAdminNetworkPolicyResource(%s)", groupResource)
	}

	for groupResource := range expectedANP {
		assert.True(t, slices.Contains(resourceList, groupResource), "expected ANP resource %s must be in resourceList", groupResource)
	}
}

func TestResourceListEgressDispatchConsistency(t *testing.T) {
	expectedEgress := map[schema.GroupResource]bool{
		gr(ovn.APIGroup, "egressfirewalls"): true,
		gr(ovn.APIGroup, "egressips"):       true,
	}

	for _, groupResource := range resourceList {
		isEgress := ovn.IsEgressResource(groupResource.Group, groupResource.Resource)
		assert.Equal(t, expectedEgress[groupResource], isEgress, "IsEgressResource(%s)", groupResource)
	}

	for groupResource := range expectedEgress {
		assert.True(t, slices.Contains(resourceList, groupResource), "expected Egress resource %s must be in resourceList", groupResource)
		assert.False(t, slices.Contains(ManagedResources, groupResource), "Egress resource %s must not be operator-managed", groupResource)
	}
}

func TestSetProcessingResources_Integration(t *testing.T) {
	// Reset state
	stream.SetProcessingResources(false)

	t.Run("sets processing to true and server is healthy", func(t *testing.T) {
		stream.SetProcessingResources(true)
		assert.True(t, stream.ServerIsHealthy())
		stream.SetProcessingResources(false)
	})

	t.Run("sets processing to false and server is healthy", func(t *testing.T) {
		stream.SetProcessingResources(false)
		assert.True(t, stream.ServerIsHealthy())
	})
}
