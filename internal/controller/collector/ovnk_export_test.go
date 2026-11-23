// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

const (
	testExportNamespace = "illumio-cloud"
	testExportPodName   = "cloud-operator-5d8f7c6b9-abcde"
)

// exportTestPod returns the operator's pod.
func exportTestPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testExportPodName,
			Namespace: testExportNamespace,
			Labels:    map[string]string{"app": "cloud-operator", "release": "illumio"},
		},
	}
}

// exportTestService returns a Service that exposes UDP port 4739 at clusterIPs
// and selects the pods of the given release.
func exportTestService(name, release string, clusterIPs ...string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testExportNamespace},
		Spec: corev1.ServiceSpec{
			ClusterIP:  clusterIPs[0],
			ClusterIPs: clusterIPs,
			Selector:   map[string]string{"app": "cloud-operator", "release": release},
			Ports: []corev1.ServicePort{
				{Name: "ipfix-collector", Protocol: corev1.ProtocolUDP, Port: 4739, TargetPort: intstr.FromInt32(4739)},
				{Name: "syslog-collector", Protocol: corev1.ProtocolTCP, Port: 6514, TargetPort: intstr.FromInt32(6514)},
			},
		},
	}
}

// exportTestNetwork returns OpenShift's network configuration. It exports flows
// to the given collectors, or has no exportNetworkFlows if there are none.
func exportTestNetwork(collectors ...string) *unstructured.Unstructured {
	network := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "operator.openshift.io/v1",
		"kind":       "Network",
		"metadata":   map[string]any{"name": openShiftNetworkName},
		"spec":       map[string]any{},
	}}

	if len(collectors) > 0 {
		values := make([]any, len(collectors))
		for i, collector := range collectors {
			values[i] = collector
		}

		_ = unstructured.SetNestedSlice(network.Object, values, "spec", "exportNetworkFlows", "ipfix", "collectors")
	}

	return network
}

// newExportTestChecker creates a checker on fake clients holding the given
// Kubernetes objects and, if not nil, the OpenShift network configuration.
func newExportTestChecker(t *testing.T, network *unstructured.Unstructured, objects ...runtime.Object) (*IPFIXExportChecker, *dynamicfake.FakeDynamicClient, *observer.ObservedLogs) {
	t.Helper()

	var networks []runtime.Object
	if network != nil {
		networks = append(networks, network)
	}

	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), networks...)
	core, logs := observer.New(zapcore.DebugLevel)

	checker, err := NewIPFIXExportChecker(zap.New(core), fake.NewClientset(objects...), dynamicClient,
		testExportNamespace, testExportPodName, "4739")
	require.NoError(t, err)

	return checker, dynamicClient, logs
}

func TestIPFIXExportChecker_Check(t *testing.T) {
	tests := map[string]struct {
		network       *unstructured.Unstructured
		objects       []runtime.Object
		wantState     ipfixExportState
		wantLevel     zapcore.Level
		wantLog       string
		wantExpected  []string
		wantCollector []string
	}{
		"export to the Service is OK": {
			network:       exportTestNetwork("172.30.10.20:4739"),
			objects:       []runtime.Object{exportTestPod(), exportTestService("illumio-cloud-operator", "illumio", "172.30.10.20")},
			wantState:     ipfixExportOK,
			wantLevel:     zapcore.InfoLevel,
			wantLog:       "OVN-Kubernetes exports network flows to the IPFIX collector",
			wantExpected:  []string{"172.30.10.20:4739"},
			wantCollector: []string{"172.30.10.20:4739"},
		},
		"one of several collectors is enough": {
			network:       exportTestNetwork("10.1.2.3:2055", "172.30.10.20:4739"),
			objects:       []runtime.Object{exportTestPod(), exportTestService("illumio-cloud-operator", "illumio", "172.30.10.20")},
			wantState:     ipfixExportOK,
			wantLevel:     zapcore.InfoLevel,
			wantLog:       "OVN-Kubernetes exports network flows to the IPFIX collector",
			wantExpected:  []string{"172.30.10.20:4739"},
			wantCollector: []string{"10.1.2.3:2055", "172.30.10.20:4739"},
		},
		"IPv6 addresses are compared in canonical form": {
			network:       exportTestNetwork("[FD02:0:0:0:0:0:0:1234]:4739"),
			objects:       []runtime.Object{exportTestPod(), exportTestService("illumio-cloud-operator", "illumio", "172.30.10.20", "fd02::1234")},
			wantState:     ipfixExportOK,
			wantLevel:     zapcore.InfoLevel,
			wantLog:       "OVN-Kubernetes exports network flows to the IPFIX collector",
			wantExpected:  []string{"172.30.10.20:4739", "[fd02::1234]:4739"},
			wantCollector: []string{"[FD02:0:0:0:0:0:0:1234]:4739"},
		},
		"export to the cluster IP of an earlier Service is reported": {
			network:       exportTestNetwork("172.30.99.99:4739"),
			objects:       []runtime.Object{exportTestPod(), exportTestService("illumio-cloud-operator", "illumio", "172.30.10.20")},
			wantState:     ipfixExportElsewhere,
			wantLevel:     zapcore.ErrorLevel,
			wantLog:       "OVN-Kubernetes exports network flows to an address that does not lead to this pod",
			wantExpected:  []string{"172.30.10.20:4739"},
			wantCollector: []string{"172.30.99.99:4739"},
		},
		"export to a Service that does not select the pod is reported": {
			network: exportTestNetwork("172.30.99.99:4739"),
			objects: []runtime.Object{
				exportTestPod(),
				exportTestService("illumio-cloud-operator", "illumio", "172.30.10.20"),
				exportTestService("release-name-cloud-operator", "release-name", "172.30.99.99"),
			},
			wantState:     ipfixExportElsewhere,
			wantLevel:     zapcore.ErrorLevel,
			wantLog:       "OVN-Kubernetes exports network flows to an address that does not lead to this pod",
			wantExpected:  []string{"172.30.10.20:4739"},
			wantCollector: []string{"172.30.99.99:4739"},
		},
		"export to another port is reported": {
			network:       exportTestNetwork("172.30.10.20:2055"),
			objects:       []runtime.Object{exportTestPod(), exportTestService("illumio-cloud-operator", "illumio", "172.30.10.20")},
			wantState:     ipfixExportElsewhere,
			wantLevel:     zapcore.ErrorLevel,
			wantLog:       "OVN-Kubernetes exports network flows to an address that does not lead to this pod",
			wantExpected:  []string{"172.30.10.20:4739"},
			wantCollector: []string{"172.30.10.20:2055"},
		},
		"no export is reported": {
			network:      exportTestNetwork(),
			objects:      []runtime.Object{exportTestPod(), exportTestService("illumio-cloud-operator", "illumio", "172.30.10.20")},
			wantState:    ipfixExportNotConfigured,
			wantLevel:    zapcore.ErrorLevel,
			wantLog:      "OVN-Kubernetes does not export network flows",
			wantExpected: []string{"172.30.10.20:4739"},
		},
		"no Service for the pod is reported": {
			network:       exportTestNetwork("172.30.99.99:4739"),
			objects:       []runtime.Object{exportTestPod(), exportTestService("release-name-cloud-operator", "release-name", "172.30.99.99")},
			wantState:     ipfixExportNoService,
			wantLevel:     zapcore.WarnLevel,
			wantLog:       "No Service leads to the IPFIX collector of this pod",
			wantCollector: []string{"172.30.99.99:4739"},
		},
		"a headless Service has no address": {
			network:       exportTestNetwork("172.30.99.99:4739"),
			objects:       []runtime.Object{exportTestPod(), exportTestService("illumio-cloud-operator", "illumio", corev1.ClusterIPNone)},
			wantState:     ipfixExportNoService,
			wantLevel:     zapcore.WarnLevel,
			wantLog:       "No Service leads to the IPFIX collector of this pod",
			wantCollector: []string{"172.30.99.99:4739"},
		},
		"a cluster that is not OpenShift is not checked": {
			objects:   []runtime.Object{exportTestPod(), exportTestService("illumio-cloud-operator", "illumio", "172.30.10.20")},
			wantState: ipfixExportUncheckable,
			wantLevel: zapcore.DebugLevel,
			wantLog:   "No OpenShift network configuration found",
		},
		"a missing pod is not checked": {
			network:   exportTestNetwork("172.30.10.20:4739"),
			objects:   []runtime.Object{exportTestService("illumio-cloud-operator", "illumio", "172.30.10.20")},
			wantState: ipfixExportUncheckable,
			wantLevel: zapcore.WarnLevel,
			wantLog:   "Failed to find this pod",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			checker, _, logs := newExportTestChecker(t, test.network, test.objects...)

			assert.Equal(t, test.wantState, checker.check(context.Background()))

			require.Equal(t, 1, logs.Len(), "logs: %v", logs.All())

			entry := logs.All()[0]
			assert.Equal(t, test.wantLevel, entry.Level)
			assert.Contains(t, entry.Message, test.wantLog)

			if test.wantState != ipfixExportUncheckable {
				fields := entry.ContextMap()
				assert.ElementsMatch(t, test.wantExpected, fields["expected_collectors"])
				assert.ElementsMatch(t, test.wantCollector, fields["configured_collectors"])
			}
		})
	}
}

// TestIPFIXExportChecker_LogsOnlyChanges checks that a lasting problem is logged
// once, and that a change of the export is logged again.
func TestIPFIXExportChecker_LogsOnlyChanges(t *testing.T) {
	checker, dynamicClient, logs := newExportTestChecker(t, exportTestNetwork("172.30.99.99:4739"),
		exportTestPod(), exportTestService("illumio-cloud-operator", "illumio", "172.30.10.20"))

	for range 3 {
		assert.Equal(t, ipfixExportElsewhere, checker.check(context.Background()))
	}

	require.Equal(t, 1, logs.Len())

	_, err := dynamicClient.Resource(openShiftNetworkGVR).Update(context.Background(),
		exportTestNetwork("172.30.10.20:4739"), metav1.UpdateOptions{})
	require.NoError(t, err)

	for range 3 {
		assert.Equal(t, ipfixExportOK, checker.check(context.Background()))
	}

	require.Equal(t, 2, logs.Len())
	assert.Equal(t, zapcore.InfoLevel, logs.All()[1].Level)
}

// TestIPFIXExportChecker_ReadErrors checks that the export is never checked
// again if the operator may not read the network configuration, and is checked
// again after any other error.
func TestIPFIXExportChecker_ReadErrors(t *testing.T) {
	tests := map[string]struct {
		err       error
		wantState ipfixExportState
		wantLog   string
	}{
		"forbidden": {
			err:       apierrors.NewForbidden(openShiftNetworkGVR.GroupResource(), openShiftNetworkName, errors.New("no access")),
			wantState: ipfixExportUncheckable,
			wantLog:   "Not allowed to read network.operator.openshift.io/cluster",
		},
		"server error": {
			err:       apierrors.NewInternalError(errors.New("etcd is down")),
			wantState: ipfixExportUnknown,
			wantLog:   "Failed to read network.operator.openshift.io/cluster",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			checker, dynamicClient, logs := newExportTestChecker(t, exportTestNetwork("172.30.10.20:4739"), exportTestPod())

			dynamicClient.PrependReactor("get", "networks", func(clienttesting.Action) (bool, runtime.Object, error) {
				return true, nil, test.err
			})

			assert.Equal(t, test.wantState, checker.check(context.Background()))

			require.Equal(t, 1, logs.Len())
			assert.Equal(t, zapcore.WarnLevel, logs.All()[0].Level)
			assert.Contains(t, logs.All()[0].Message, test.wantLog)
		})
	}
}

// TestIPFIXExportChecker_RunStopsWhenUncheckable checks that Run returns, and
// stops checking, on a cluster that is not OpenShift.
func TestIPFIXExportChecker_RunStopsWhenUncheckable(t *testing.T) {
	checker, _, _ := newExportTestChecker(t, nil, exportTestPod())

	done := make(chan struct{})

	go func() {
		checker.Run(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
	}
}

func TestNewIPFIXExportChecker_InvalidPort(t *testing.T) {
	_, err := NewIPFIXExportChecker(zap.NewNop(), fake.NewClientset(), dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		testExportNamespace, testExportPodName, "ipfix")
	require.Error(t, err)
}
