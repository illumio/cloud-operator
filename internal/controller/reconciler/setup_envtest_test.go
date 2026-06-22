// Copyright 2026 Illumio, Inc. All Rights Reserved.

//go:build envtest

package reconciler

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/illumio/cloud-operator/fakeserver"
	"github.com/illumio/cloud-operator/internal/controller/k8sclient"
	"github.com/illumio/cloud-operator/internal/controller/logging"
	"github.com/illumio/cloud-operator/internal/controller/stream"
	"github.com/illumio/cloud-operator/internal/controller/stream/config"
	"github.com/illumio/cloud-operator/internal/controller/stream/config/cache"
	"github.com/illumio/cloud-operator/internal/controller/stream/resources"
)

var (
	testEnv    *envtest.Environment
	testClient k8sclient.Client
)

var cnpGVR = schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: "ciliumnetworkpolicies"}
var ccnpGVR = schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: "ciliumclusterwidenetworkpolicies"}
var cidrGroupGVR = schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: "ciliumcidrgroups"}

func TestMain(m *testing.M) {
	// Set KUBEBUILDER_ASSETS if not already set, so tests work from IDEs
	// without needing to manually export the variable.
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		out, err := exec.Command("setup-envtest", "use", "--print", "path").Output()
		if err != nil {
			fmt.Fprintf(os.Stderr, "KUBEBUILDER_ASSETS not set and setup-envtest not available: %v\n", err)
			os.Exit(1)
		}

		os.Setenv("KUBEBUILDER_ASSETS", strings.TrimSpace(string(out)))
	}

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join(testdataDir(), "crds")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start envtest: %v\n", err)
		os.Exit(1)
	}
	defer testEnv.Stop() //nolint:errcheck

	testClient, err = k8sclient.NewClientFromConfig(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create k8s client: %v\n", err)
		os.Exit(1)
	}

	m.Run()
}

func testdataDir() string {
	_, filename, _, _ := runtime.Caller(0)

	return filepath.Join(filepath.Dir(filename), "testdata")
}

// newTestHarness creates a FakeServerTestHarness with AutoHandshake disabled
// (reconciler integration tests control the handshake sequence themselves).
func newTestHarness(t *testing.T) *fakeserver.FakeServerTestHarness {
	t.Helper()

	cfg := fakeserver.DefaultTestConfig()
	cfg.GRPCAddress = "127.0.0.1:0"
	cfg.HTTPAddress = "127.0.0.1:0"
	cfg.AutoHandshake = false
	cfg.EnableLogging = false

	h := fakeserver.NewTestHarness(t, cfg)
	require.NoError(t, h.Start())
	t.Cleanup(h.Stop)

	return h
}

// setupSuite wires the full production pipeline against envtest:
//
//	fakeserver (gRPC) → config client → config cache ──→ reconciler → envtest K8s API
//	                     resources client → runtime cache ─┘
func setupSuite(t *testing.T) *fakeserver.FakeServer {
	t.Helper()

	return setupSuiteWithReconcilerLogger(t, zap.NewNop())
}

// setupSuiteWithReconcilerLogger is identical to setupSuite but injects the given
// logger into the reconciler, so tests can observe reconciler log output.
func setupSuiteWithReconcilerLogger(t *testing.T, reconcilerLogger *zap.Logger) *fakeserver.FakeServer {
	t.Helper()

	configCache := cache.NewConfiguredObjectCache()
	runtimeCache := cache.NewConfiguredObjectCache()

	h := newTestHarness(t)
	conn := h.DialGRPC(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	// Start resources stream client (watches envtest K8s API → populates runtime cache → streams to fakeserver)
	resourcesFactory := &resources.Factory{
		Logger:    zap.NewNop(),
		K8sClient: testClient,
		Stats:     stream.NewStats(),
		Cache:     runtimeCache,
	}
	resourcesClient, err := resourcesFactory.NewStreamClient(ctx, conn)
	require.NoError(t, err)

	go resourcesClient.Run(ctx)

	// Start config stream client (fakeserver → config cache)
	configFactory := &config.Factory{
		Logger:             zap.NewNop(),
		BufferedGrpcSyncer: logging.NewBufferedGrpcWriteSyncerForTest(zap.NewNop()),
		Stats:              stream.NewStats(),
		Cache:              configCache,
	}
	configClient, err := configFactory.NewStreamClient(ctx, conn)
	require.NoError(t, err)

	go configClient.Run(ctx)

	// Start reconciler (config cache + runtime cache → K8s API)
	r := NewReconciler(reconcilerLogger, testClient, configCache, runtimeCache)
	go r.Run(ctx)

	return h.Server
}
