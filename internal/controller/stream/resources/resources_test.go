// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakediscovery "k8s.io/client-go/discovery/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/convert/anp"
	"github.com/illumio/cloud-operator/internal/convert/awsvpccni"
	"github.com/illumio/cloud-operator/internal/convert/cilium"
	"github.com/illumio/cloud-operator/internal/convert/ovn"
)

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

		resources := []string{"pods", "deployments"}

		result, err := BuildResourceAPIGroupMap(resources, clientset, logger)
		require.NoError(t, err)

		// pods should be in core group (empty string)
		resourceInfo, ok := result["pods"]
		assert.True(t, ok, "expected 'pods' to be in result")
		assert.Empty(t, resourceInfo.Group, "expected pods apiGroup to be empty")
		assert.Equal(t, "v1", resourceInfo.Version)
	})

	t.Run("handles empty resources", func(t *testing.T) {
		clientset := k8sfake.NewSimpleClientset()

		resources := []string{}

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

		resources := []string{"nodes"}

		result, err := BuildResourceAPIGroupMap(resources, clientset, logger)
		require.NoError(t, err)

		// nodes should NOT be mapped because metrics.k8s.io is skipped
		_, ok = result["nodes"]
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

		result, err := BuildResourceAPIGroupMap([]string{"deployments"}, clientset, logger)
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

		resources := []string{"deployments", "statefulsets"}

		result, err := BuildResourceAPIGroupMap(resources, clientset, logger)
		require.NoError(t, err)

		assert.Equal(t, "apps", result["deployments"].Group)
		assert.Equal(t, "v1", result["deployments"].Version)
		assert.Equal(t, "apps", result["statefulsets"].Group)
		assert.Equal(t, "v1", result["statefulsets"].Version)
	})
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
	supported := []*metav1.APIResourceList{
		{GroupVersion: "policy.networking.k8s.io/v1alpha1", APIResources: policyResources},
		{GroupVersion: "k8s.ovn.org/v1", APIResources: egressResources},
	}
	foreign := &metav1.APIResourceList{
		GroupVersion: "example.com/v1",
		APIResources: slices.Concat(policyResources, egressResources),
	}
	wantSupported := map[string]ResourceInfo{
		"adminnetworkpolicies":         {Group: "policy.networking.k8s.io", Version: "v1alpha1"},
		"baselineadminnetworkpolicies": {Group: "policy.networking.k8s.io", Version: "v1alpha1"},
		"egressfirewalls":              {Group: "k8s.ovn.org", Version: "v1"},
		"egressips":                    {Group: "k8s.ovn.org", Version: "v1"},
	}

	tests := []struct {
		name      string
		resources []*metav1.APIResourceList
		want      map[string]ResourceInfo
	}{
		{name: "supported groups", resources: supported, want: wantSupported},
		{name: "foreign group only", resources: []*metav1.APIResourceList{foreign}, want: map[string]ResourceInfo{}},
		{name: "foreign group listed first", resources: append([]*metav1.APIResourceList{foreign}, supported...), want: wantSupported},
		{name: "foreign group listed last", resources: append(slices.Clone(supported), foreign), want: wantSupported},
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

func TestResourceListCiliumDispatchConsistency(t *testing.T) {
	expectedCilium := map[string]bool{
		"ciliumcidrgroups":                 true,
		"ciliumclusterwidenetworkpolicies": true,
		"ciliumnetworkpolicies":            true,
	}

	for _, resource := range resourceList {
		isCilium := cilium.IsCiliumResource(resource)

		if expectedCilium[resource] {
			assert.True(t, isCilium, "resource %q should be recognized as Cilium by IsCiliumResource", resource)
		} else {
			assert.False(t, isCilium, "resource %q should NOT be recognized as Cilium by IsCiliumResource", resource)
		}
	}

	for name := range expectedCilium {
		assert.True(t, slices.Contains(resourceList, name), "expected Cilium resource %q must be in resourceList", name)
	}
}

func TestResourceListAWSDispatchConsistency(t *testing.T) {
	// Both AWS policy resources are watched (ingested) and routed to the AWS converter.
	awsResources := []string{"clusternetworkpolicies", "applicationnetworkpolicies"}

	for _, name := range awsResources {
		assert.True(t, slices.Contains(resourceList, name),
			"%s must be in resourceList (ingested)", name)
		assert.True(t, awsvpccni.IsAWSResource(name),
			"%s must be recognized by IsAWSResource", name)
	}

	// ClusterNetworkPolicy is enforced/reconciled, so it must be in ManagedResourceNames.
	assert.True(t, slices.Contains(ManagedResourceNames, "clusternetworkpolicies"),
		"clusternetworkpolicies must be in ManagedResourceNames (enforced)")

	// ApplicationNetworkPolicy is ingest-only (never enforced), so it must NOT be in
	// ManagedResourceNames, otherwise the reconciler would try to apply/delete it.
	assert.False(t, slices.Contains(ManagedResourceNames, "applicationnetworkpolicies"),
		"applicationnetworkpolicies must NOT be in ManagedResourceNames (ingest-only)")

	// Cilium resources must not be misrouted to the AWS converter.
	for _, resource := range resourceList {
		if cilium.IsCiliumResource(resource) {
			assert.False(t, awsvpccni.IsAWSResource(resource),
				"resource %q should not be recognized as both Cilium and AWS", resource)
		}
	}
}

func TestResourceListAdminNetworkPolicyDispatchConsistency(t *testing.T) {
	expectedANP := map[string]bool{
		"adminnetworkpolicies":         true,
		"baselineadminnetworkpolicies": true,
	}

	for _, resource := range resourceList {
		isANP := anp.IsAdminNetworkPolicyResource("policy.networking.k8s.io", resource)

		if expectedANP[resource] {
			assert.True(t, isANP, "resource %q should be recognized as ANP by IsAdminNetworkPolicyResource", resource)
		} else {
			assert.False(t, isANP, "resource %q should NOT be recognized as ANP by IsAdminNetworkPolicyResource", resource)
		}
	}

	for name := range expectedANP {
		assert.True(t, slices.Contains(resourceList, name), "expected ANP resource %q must be in resourceList", name)
	}
}

func TestResourceListEgressDispatchConsistency(t *testing.T) {
	expectedEgress := map[string]bool{
		"egressfirewalls": true,
		"egressips":       true,
	}

	for _, resource := range resourceList {
		isEgress := ovn.IsEgressResource("k8s.ovn.org", resource)

		if expectedEgress[resource] {
			assert.True(t, isEgress, "resource %q should be recognized as Egress by IsEgressResource", resource)
		} else {
			assert.False(t, isEgress, "resource %q should NOT be recognized as Egress by IsEgressResource", resource)
		}
	}

	for name := range expectedEgress {
		assert.True(t, slices.Contains(resourceList, name), "expected Egress resource %q must be in resourceList", name)
		assert.False(t, slices.Contains(ManagedResourceNames, name), "Egress resource %q must not be operator-managed", name)
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
