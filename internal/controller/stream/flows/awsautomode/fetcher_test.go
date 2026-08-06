// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsautomode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// newTestClientset builds a kubernetes.Interface whose REST client is backed by
// the given httptest.Server, so restLogFetcher exercises the real request path
// (nodes/{name}/proxy/logs/...) without a live cluster.
func newTestClientset(t *testing.T, srv *httptest.Server) kubernetes.Interface {
	t.Helper()

	cfg := &rest.Config{Host: srv.URL}
	cs, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)

	return cs
}

func TestRestLogFetcher_BuildsNodeProxyPath(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte("log-bytes"))
	}))
	defer srv.Close()

	f := &restLogFetcher{k8sClient: newTestClientset(t, srv)}
	data, err := f.fetch(context.Background(), "node-a")

	require.NoError(t, err)
	assert.Equal(t, "log-bytes", string(data))
	assert.Equal(t,
		"/api/v1/nodes/node-a/proxy/logs/aws-routed-eni/network-policy-agent.log",
		gotPath,
	)
}

// TestRestLogFetcher_LogsRequestURL verifies the fetcher emits a Debug log
// carrying the constructed node-proxy URL, so operators can see the exact path
// requested when troubleshooting.
func TestRestLogFetcher_LogsRequestURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("log-bytes"))
	}))
	defer srv.Close()

	core, logs := observer.New(zap.DebugLevel)

	f := &restLogFetcher{k8sClient: newTestClientset(t, srv), logger: zap.New(core)}
	_, err := f.fetch(context.Background(), "node-a")
	require.NoError(t, err)

	entries := logs.FilterMessage("EKS Auto Mode fetching node-proxy log").All()
	require.Len(t, entries, 1)

	fields := entries[0].ContextMap()
	assert.Equal(t, "node-a", fields["node"])
	assert.Contains(t, fields["url"],
		"/api/v1/nodes/node-a/proxy/logs/aws-routed-eni/network-policy-agent.log")
}

func TestRestLogFetcher_HonorsCustomLogPath(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
	}))
	defer srv.Close()

	f := &restLogFetcher{k8sClient: newTestClientset(t, srv), logPath: "custom/agent.log"}
	_, err := f.fetch(context.Background(), "node-b")

	require.NoError(t, err)
	assert.Equal(t, "/api/v1/nodes/node-b/proxy/logs/custom/agent.log", gotPath)
}

func TestRestLogFetcher_PropagatesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	f := &restLogFetcher{k8sClient: newTestClientset(t, srv)}
	_, err := f.fetch(context.Background(), "node-a")

	assert.Error(t, err)
}

// TestIntegration_PollSequence drives a full poll sequence through the real REST
// fetcher: initial two records, incremental append, rotation-with-shrink, then a
// node restart (new UID). It asserts each cycle emits only the expected new flows.
func TestIntegration_PollSequence(t *testing.T) {
	// The server returns a body controlled per-request via a closure variable and
	// records the last requested path for assertion in the test goroutine (asserting
	// inside the handler goroutine would violate testify's require contract).
	var (
		body    string
		gotPath string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	logger := zap.NewNop()
	sink := &mockFlowSink{}
	sink.On("CacheFlow", mock.Anything, mock.Anything).Return(nil)
	sink.On("IncrementFlowsReceived").Return()

	c := &autoModeClient{
		logger:             logger,
		flowSink:           sink,
		fetcher:            &restLogFetcher{k8sClient: newTestClientset(t, srv)},
		maxConcurrentPolls: 1,
		checkpoints:        newCheckpointStore(),
	}

	ctx := context.Background()

	// Cycle 1: two flows.
	body = oldFmtFlow1 + "\n" + oldFmtFlow2 + "\n"

	require.NoError(t, c.pollNode(ctx, "node-a", "uid-1", logger))
	sink.AssertNumberOfCalls(t, "CacheFlow", 2)
	assert.True(t, strings.HasSuffix(gotPath, "network-policy-agent.log"))

	// Cycle 2: append a third; only it is new.
	body = oldFmtFlow1 + "\n" + oldFmtFlow2 + "\n" + oldFmtFlow3 + "\n"

	require.NoError(t, c.pollNode(ctx, "node-a", "uid-1", logger))
	sink.AssertNumberOfCalls(t, "CacheFlow", 3)

	// Cycle 3: rotation-with-shrink (smaller file, one flow) -> reprocessed.
	body = oldFmtFlow1 + "\n"

	require.NoError(t, c.pollNode(ctx, "node-a", "uid-1", logger))
	sink.AssertNumberOfCalls(t, "CacheFlow", 4)

	// Cycle 4: node restart (new UID), same single flow -> reprocessed.
	require.NoError(t, c.pollNode(ctx, "node-a", "uid-2", logger))
	sink.AssertNumberOfCalls(t, "CacheFlow", 5)
}
