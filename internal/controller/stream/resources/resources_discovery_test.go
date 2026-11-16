// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// stubDiscovery serves a fixed group list and per-GroupVersion resource lists
// or errors, and records which GroupVersions were queried.
type stubDiscovery struct {
	discovery.DiscoveryInterfaces

	groups    []metav1.APIGroup
	resources map[string][]metav1.APIResource
	errs      map[string]error
	queried   []string
}

func (d *stubDiscovery) ServerGroups() (*metav1.APIGroupList, error) {
	return &metav1.APIGroupList{Groups: d.groups}, nil
}

func (d *stubDiscovery) ServerResourcesForGroupVersion(groupVersion string) (*metav1.APIResourceList, error) {
	d.queried = append(d.queried, groupVersion)

	if groupVersion == "" {
		// Same error as the real discovery client.
		return nil, errors.New("groupVersion shouldn't be empty")
	}

	if err, ok := d.errs[groupVersion]; ok {
		return nil, err
	}

	return &metav1.APIResourceList{GroupVersion: groupVersion, APIResources: d.resources[groupVersion]}, nil
}

// stubClientset is a clientset whose discovery client is a stubDiscovery.
type stubClientset struct {
	*k8sfake.Clientset

	discovery *stubDiscovery
}

func (c *stubClientset) Discovery() discovery.DiscoveryInterfaces {
	return c.discovery
}

func newStubClientset(d *stubDiscovery) *stubClientset {
	return &stubClientset{Clientset: k8sfake.NewClientset(), discovery: d}
}

// apiGroup builds a group whose preferred version is the first of versions. With
// no versions the preferred version is empty, which is what client-go returns
// for an aggregated API whose every version is stale (APIService unavailable).
func apiGroup(name string, versions ...string) metav1.APIGroup {
	group := metav1.APIGroup{Name: name}

	for _, version := range versions {
		group.Versions = append(group.Versions, metav1.GroupVersionForDiscovery{
			GroupVersion: schema.GroupVersion{Group: name, Version: version}.String(),
			Version:      version,
		})
	}

	if len(group.Versions) > 0 {
		group.PreferredVersion = group.Versions[0]
	}

	return group
}

var errServiceUnavailable = errors.New("the server is currently unable to handle the request")

// A broken API the operator does not watch (an unavailable aggregated
// APIService such as custom.metrics.k8s.io) must not fail discovery.
func TestBuildResourceAPIGroupMap_ToleratesUnwatchedBrokenGroups(t *testing.T) {
	wanted := []schema.GroupResource{gr("", "pods"), gr("apps", "deployments")}

	for _, searchAllVersions := range []bool{false, true} {
		d := &stubDiscovery{
			groups: []metav1.APIGroup{
				apiGroup("", "v1"),
				// Every version stale: client-go keeps the group with an empty PreferredVersion.
				apiGroup("custom.metrics.k8s.io"),
				// Listed, but its discovery document cannot be fetched.
				apiGroup("metrics.k8s.io", "v1beta1"),
				apiGroup("apps", "v1"),
			},
			resources: map[string][]metav1.APIResource{
				"v1":      {{Name: "pods", Kind: "Pod"}},
				"apps/v1": {{Name: "deployments", Kind: "Deployment"}},
			},
			errs: map[string]error{"metrics.k8s.io/v1beta1": errServiceUnavailable},
		}

		result, err := buildResourceAPIGroupMap(wanted, newStubClientset(d), zap.NewNop(), searchAllVersions)
		require.NoError(t, err)

		assert.Equal(t, map[schema.GroupResource]ResourceInfo{
			gr("", "pods"):            {Group: "", Version: "v1"},
			gr("apps", "deployments"): {Group: "apps", Version: "v1"},
		}, result)
		assert.Equal(t, []string{"v1", "apps/v1"}, d.queried, "only groups with wanted resources are discovered")
	}
}

// A watched group with no available version is skipped: its resources are
// simply not served right now.
func TestBuildResourceAPIGroupMap_SkipsWatchedGroupWithoutPreferredVersion(t *testing.T) {
	d := &stubDiscovery{
		groups: []metav1.APIGroup{apiGroup("", "v1"), apiGroup("metallb.io")},
		resources: map[string][]metav1.APIResource{
			"v1": {{Name: "pods", Kind: "Pod"}},
		},
	}

	wanted := []schema.GroupResource{gr("", "pods"), gr("metallb.io", "servicel2statuses")}

	result, err := buildResourceAPIGroupMap(wanted, newStubClientset(d), zap.NewNop(), true)
	require.NoError(t, err)

	assert.Equal(t, map[schema.GroupResource]ResourceInfo{gr("", "pods"): {Group: "", Version: "v1"}}, result)
	assert.Equal(t, []string{"v1"}, d.queried)
}

// A discovery failure in a group the operator does watch is still an error, so
// the caller retries instead of streaming an incomplete set of resources.
func TestBuildResourceAPIGroupMap_FailsOnWatchedGroupError(t *testing.T) {
	d := &stubDiscovery{
		groups: []metav1.APIGroup{apiGroup("", "v1"), apiGroup("apps", "v1")},
		resources: map[string][]metav1.APIResource{
			"v1": {{Name: "pods", Kind: "Pod"}},
		},
		errs: map[string]error{"apps/v1": errServiceUnavailable},
	}

	wanted := []schema.GroupResource{gr("", "pods"), gr("apps", "deployments")}

	_, err := buildResourceAPIGroupMap(wanted, newStubClientset(d), zap.NewNop(), true)
	require.ErrorIs(t, err, errServiceUnavailable)
}
