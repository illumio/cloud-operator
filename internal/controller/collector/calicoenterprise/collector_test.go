// Copyright 2026 Illumio, Inc. All Rights Reserved.

package calicoenterprise

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

func TestIsDeployed(t *testing.T) {
	installation := func(name string, spec map[string]any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "operator.tigera.io/v1",
			"kind":       "Installation",
			"metadata":   map[string]any{"name": name},
			"spec":       spec,
		}}
	}

	logCollector := func(spec map[string]any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "operator.tigera.io/v1",
			"kind":       "LogCollector",
			"metadata":   map[string]any{"name": "tigera-secure"},
			"spec":       spec,
		}}
	}

	syslogStore := func(endpoint string, logTypes ...any) map[string]any {
		return map[string]any{"additionalStores": map[string]any{"syslog": map[string]any{
			"endpoint": endpoint,
			"logTypes": logTypes,
		}}}
	}

	enterprise := installation("default", map[string]any{"variant": "TigeraSecureEnterprise"})

	logCollectorGR := logCollectorGVR.GroupResource()

	tests := map[string]struct {
		objects         []runtime.Object
		logCollectorErr error
		expectedResult  bool
		expectWarning   string
		expectErrorText string
	}{
		"Enterprise with a syslog store sending flows to the operator's port": {
			objects:        []runtime.Object{enterprise, logCollector(syslogStore("tcp://illumio-cloud-operator.illumio-cloud.svc.cluster.local:6514", "Flows"))},
			expectedResult: true,
		},
		"Enterprise with a syslog store on another port is picked with a warning": {
			objects:        []runtime.Object{enterprise, logCollector(syslogStore("tcp://10.0.0.1:601", "Flows"))},
			expectedResult: true,
			expectWarning:  "port",
		},
		"Enterprise without a LogCollector is not picked": {
			objects:       []runtime.Object{enterprise},
			expectWarning: "syslog",
		},
		"Enterprise with a LogCollector without a syslog store is not picked": {
			objects:       []runtime.Object{enterprise, logCollector(map[string]any{"collectProcessPath": "Enabled"})},
			expectWarning: "syslog",
		},
		"Enterprise whose LogCollector cannot be read due to RBAC is not picked and reports the real error": {
			objects:         []runtime.Object{enterprise, logCollector(syslogStore("tcp://10.0.0.1:6514", "Flows"))},
			logCollectorErr: apierrors.NewForbidden(logCollectorGR, "tigera-secure", nil),
			expectWarning:   "Failed to read the Calico Enterprise LogCollector",
			expectErrorText: "forbidden",
		},
		"Enterprise whose LogCollector cannot be read due to a server error is not picked and reports the real error": {
			objects:         []runtime.Object{enterprise, logCollector(syslogStore("tcp://10.0.0.1:6514", "Flows"))},
			logCollectorErr: apierrors.NewServerTimeout(logCollectorGR, "get", 1),
			expectWarning:   "Failed to read the Calico Enterprise LogCollector",
			expectErrorText: "could not be completed",
		},
		"Enterprise with a syslog store that does not send flows is not picked": {
			objects:       []runtime.Object{enterprise, logCollector(syslogStore("tcp://10.0.0.1:6514", "Audit", "DNS"))},
			expectWarning: "Flows",
		},
		"Installation with the open-source Calico variant": {
			objects: []runtime.Object{installation("default", map[string]any{"variant": "Calico"}), logCollector(syslogStore("tcp://10.0.0.1:6514", "Flows"))},
		},
		"Installation without a variant": {
			objects: []runtime.Object{installation("default", map[string]any{})},
		},
		"Enterprise Installation with a different name is ignored": {
			objects: []runtime.Object{installation("other", map[string]any{"variant": "TigeraSecureEnterprise"})},
		},
		"no Installation": {},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
				map[schema.GroupVersionResource]string{
					calicoInstallationGVR: "InstallationList",
					logCollectorGVR:       "LogCollectorList",
				},
				tt.objects...)

			if tt.logCollectorErr != nil {
				dynamicClient.PrependReactor("get", logCollectorGVR.Resource,
					func(k8stesting.Action) (bool, runtime.Object, error) {
						return true, nil, tt.logCollectorErr
					})
			}

			core, logs := observer.New(zap.WarnLevel)

			result := IsDeployed(context.TODO(), zap.New(core), dynamicClient, "6514")
			assert.Equal(t, tt.expectedResult, result)

			warnings := logs.All()
			if tt.expectWarning == "" {
				assert.Empty(t, warnings)

				return
			}

			require.Len(t, warnings, 1)
			assert.Contains(t, warnings[0].Message, tt.expectWarning)

			if tt.expectErrorText != "" {
				assert.NotContains(t, warnings[0].Message, "no syslog store")
				assert.Contains(t, warnings[0].ContextMap()["error"], tt.expectErrorText)
			}
		})
	}
}

func TestNextAcceptDelay(t *testing.T) {
	delays := make([]time.Duration, 0, 10)

	var delay time.Duration

	for range 10 {
		delay = nextAcceptDelay(delay)
		delays = append(delays, delay)
	}

	assert.Equal(t, []time.Duration{
		5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond,
		80 * time.Millisecond, 160 * time.Millisecond, 320 * time.Millisecond, 640 * time.Millisecond,
		time.Second, time.Second,
	}, delays)
}

func TestRunSyslogCollectorShutdownLogsNoError(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	collector := NewCollector(zap.New(core), "0", tls.ServerSettings{}, newRecordingSink(), nil)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() {
		done <- collector.RunSyslogCollector(ctx)
	}()

	require.Eventually(t, func() bool {
		return logs.FilterMessage("Calico Enterprise syslog collector listening").Len() > 0
	}, time.Second, 5*time.Millisecond)

	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("collector did not stop after context cancellation")
	}

	assert.Empty(t, logs.FilterLevelExact(zapcore.ErrorLevel).All(), "closing an already-closed listener must not log an error")
}
