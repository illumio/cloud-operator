// Copyright 2026 Illumio, Inc. All Rights Reserved.

package resources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestIsGKEVersion(t *testing.T) {
	cases := map[string]bool{
		"v1.34.4-gke.1193000": true,  // Autopilot / Standard control plane
		"v1.32.6-gke.1200000": true,  //
		"v1.30.0":             false, // upstream / kind
		"v1.30.2-eks-1234567": false, // EKS
		"v1.29.0+k3s1":        false, // k3s
		"":                    false,
	}

	for version, want := range cases {
		assert.Equal(t, want, isGKEVersion(version), "isGKEVersion(%q)", version)
	}
}

// gkeMetadataMux returns an httptest server that mimics the GKE metadata server:
// it requires the Metadata-Flavor header and serves the three cluster-identity
// attributes from the provided values (empty value → 404).
func gkeMetadataMux(t *testing.T, name, location, projectID string) *httptest.Server {
	t.Helper()

	serve := func(w http.ResponseWriter, r *http.Request, value string) {
		if r.Header.Get(gkeMetadataFlavorHeader) != gkeMetadataFlavorValue {
			w.WriteHeader(http.StatusForbidden)

			return
		}

		if value == "" {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		_, _ = w.Write([]byte(value))
	}

	mux := http.NewServeMux()
	mux.HandleFunc(gkeMetadataClusterNamePath, func(w http.ResponseWriter, r *http.Request) { serve(w, r, name) })
	mux.HandleFunc(gkeMetadataClusterLocationPath, func(w http.ResponseWriter, r *http.Request) { serve(w, r, location) })
	mux.HandleFunc(gkeMetadataProjectIDPath, func(w http.ResponseWriter, r *http.Request) { serve(w, r, projectID) })

	return httptest.NewServer(mux)
}

func TestResolveGKEClusterInfo(t *testing.T) {
	logger := zap.NewNop()

	t.Run("returns all three attributes", func(t *testing.T) {
		srv := gkeMetadataMux(t, "my-autopilot-cluster\n", "us-central1\n", "my-project\n")
		defer srv.Close()

		info := resolveGKEClusterInfoFrom(context.Background(), logger, srv.Client(), srv.URL)
		assert.Equal(t, "my-autopilot-cluster", info.Name, "trailing whitespace should be trimmed")
		assert.Equal(t, "us-central1", info.Location)
		assert.Equal(t, "my-project", info.ProjectID)
	})

	t.Run("partial availability leaves missing fields empty", func(t *testing.T) {
		// Location attribute missing (empty → 404); name and project present.
		srv := gkeMetadataMux(t, "c1", "", "p1")
		defer srv.Close()

		info := resolveGKEClusterInfoFrom(context.Background(), logger, srv.Client(), srv.URL)
		assert.Equal(t, "c1", info.Name)
		assert.Empty(t, info.Location)
		assert.Equal(t, "p1", info.ProjectID)
	})

	t.Run("unreachable server yields empty info without error", func(t *testing.T) {
		srv := gkeMetadataMux(t, "c1", "l1", "p1")
		url := srv.URL
		srv.Close()

		info := resolveGKEClusterInfoFrom(context.Background(), logger, http.DefaultClient, url)
		assert.Empty(t, info.Name)
		assert.Empty(t, info.Location)
		assert.Empty(t, info.ProjectID)
	})
}

func TestFetchGKEMetadata_RequiresFlavorHeader(t *testing.T) {
	logger := zap.NewNop()

	var gotFlavor string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFlavor = r.Header.Get(gkeMetadataFlavorHeader)
		_, _ = w.Write([]byte("value"))
	}))
	defer srv.Close()

	value, ok := fetchGKEMetadata(context.Background(), logger, srv.Client(), srv.URL)
	assert.True(t, ok)
	assert.Equal(t, "value", value)
	assert.Equal(t, gkeMetadataFlavorValue, gotFlavor, "Metadata-Flavor header must be sent")
}
