// Copyright 2026 Illumio, Inc. All Rights Reserved.

package k8sclient

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/client-go/rest"
)

// baseTransport unwraps the round tripper client-go builds for config down to
// the *http.Transport that dials the API server.
func baseTransport(t *testing.T, config *rest.Config) *http.Transport {
	t.Helper()

	rt, err := rest.TransportFor(config)
	require.NoError(t, err)

	for {
		if transport, ok := rt.(*http.Transport); ok {
			return transport
		}

		wrapper, ok := rt.(utilnet.RoundTripperWrapper)
		require.Truef(t, ok, "cannot unwrap %T to reach the *http.Transport", rt)

		rt = wrapper.WrappedRoundTripper()
	}
}

// proxyFor returns the proxy the transport would use to reach target.
func proxyFor(t *testing.T, transport *http.Transport, target string) *url.URL {
	t.Helper()

	if transport.Proxy == nil {
		return nil
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	require.NoError(t, err)

	proxyURL, err := transport.Proxy(req)
	require.NoError(t, err)

	return proxyURL
}

// inClusterStyleConfig returns a config shaped like rest.InClusterConfig: an
// https host verified against a CA bundle read from a file.
func inClusterStyleConfig(t *testing.T, host string) *rest.Config {
	t.Helper()

	server := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(server.Close)

	caFile := filepath.Join(t.TempDir(), "ca.crt")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	require.NoError(t, os.WriteFile(caFile, caPEM, 0o600))

	return &rest.Config{Host: host, TLSClientConfig: rest.TLSClientConfig{CAFile: caFile}}
}

// The operator's HTTPS_PROXY is meant for CloudSecure only. The Kubernetes API
// server must be reached directly, even when the chart sets no NO_PROXY.
func TestNewClientFromConfig_IgnoresProxyEnvironment(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://proxy.example.com:3128")
	t.Setenv("HTTP_PROXY", "http://proxy.example.com:3128")
	t.Setenv("NO_PROXY", "")

	const apiServer = "https://198.18.0.1:443"

	config := inClusterStyleConfig(t, apiServer)

	_, err := NewClientFromConfig(config)
	require.NoError(t, err)

	// Check the config directly: http.ProxyFromEnvironment reads the environment
	// only once per process, so a transport still using it could resolve no proxy
	// here if an earlier test read the environment before HTTPS_PROXY was set.
	require.NotNil(t, config.Proxy, "the config must set an explicit proxy function")

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, apiServer+"/api", nil)
	require.NoError(t, err)

	proxyURL, err := config.Proxy(req)
	require.NoError(t, err)
	assert.Nil(t, proxyURL, "the config must connect to the API server directly")

	assert.Nil(t, proxyFor(t, baseTransport(t, config), apiServer+"/api"),
		"Kubernetes API requests must not go through the proxy from the environment")
}

// A proxy-url set explicitly in a kubeconfig is how that API server is reached
// and must be kept.
func TestNewClientFromConfig_KeepsExplicitProxy(t *testing.T) {
	const apiServer = "https://198.18.0.1:443"

	explicit, err := url.Parse("http://kubeconfig-proxy.example.com:8080")
	require.NoError(t, err)

	config := inClusterStyleConfig(t, apiServer)
	config.Proxy = http.ProxyURL(explicit)

	_, err = NewClientFromConfig(config)
	require.NoError(t, err)

	assert.Equal(t, explicit, proxyFor(t, baseTransport(t, config), apiServer+"/api"))
}
