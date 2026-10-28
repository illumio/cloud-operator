// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestIsCalicoEnterpriseDeployed(t *testing.T) {
	installation := func(name string, spec map[string]any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "operator.tigera.io/v1",
			"kind":       "Installation",
			"metadata":   map[string]any{"name": name},
			"spec":       spec,
		}}
	}

	tests := map[string]struct {
		objects        []runtime.Object
		expectedResult bool
	}{
		"Installation with the Enterprise variant": {
			objects:        []runtime.Object{installation("default", map[string]any{"variant": "TigeraSecureEnterprise"})},
			expectedResult: true,
		},
		"Installation with the open-source Calico variant": {
			objects:        []runtime.Object{installation("default", map[string]any{"variant": "Calico"})},
			expectedResult: false,
		},
		"Installation without a variant": {
			objects:        []runtime.Object{installation("default", map[string]any{})},
			expectedResult: false,
		},
		"Enterprise Installation with a different name is ignored": {
			objects:        []runtime.Object{installation("other", map[string]any{"variant": "TigeraSecureEnterprise"})},
			expectedResult: false,
		},
		"no Installation": {
			expectedResult: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
				map[schema.GroupVersionResource]string{calicoInstallationGVR: "InstallationList"},
				tt.objects...)

			result := IsCalicoEnterpriseDeployed(context.TODO(), zap.NewNop(), dynamicClient)
			assert.Equal(t, tt.expectedResult, result)
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
	collector := NewCalicoCollector(zap.New(core), "0", CalicoSyslogTLS{}, newRecordingSink(), nil)

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
