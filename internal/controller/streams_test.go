// Copyright 2026 Illumio, Inc. All Rights Reserved.

package controller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

func TestServerIsHealthy_ReturnsTrue_WhenNotProcessing(t *testing.T) {
	// Reset global state
	dd.mutex.Lock()
	dd.processingResources = false
	dd.timeStarted = time.Time{}
	dd.mutex.Unlock()

	result := ServerIsHealthy()

	assert.True(t, result)
}

func TestServerIsHealthy_ReturnsTrue_WhenProcessingRecentlyStarted(t *testing.T) {
	// Set processing started recently
	dd.mutex.Lock()
	dd.processingResources = true
	dd.timeStarted = time.Now()
	dd.mutex.Unlock()

	result := ServerIsHealthy()

	assert.True(t, result, "Should be healthy when processing started recently")
}

func TestServerIsHealthy_ReturnsFalse_WhenProcessingTooLong(t *testing.T) {
	// Set processing started more than 5 minutes ago
	dd.mutex.Lock()
	dd.processingResources = true
	dd.timeStarted = time.Now().Add(-6 * time.Minute)
	dd.mutex.Unlock()

	result := ServerIsHealthy()

	assert.False(t, result, "Should be unhealthy when processing for more than 5 minutes")
}

func TestServerIsHealthy_ReturnsTrue_WhenNotProcessingEvenIfOldTime(t *testing.T) {
	// Not processing, but time is old (shouldn't matter)
	dd.mutex.Lock()
	dd.processingResources = false
	dd.timeStarted = time.Now().Add(-10 * time.Minute)
	dd.mutex.Unlock()

	result := ServerIsHealthy()

	assert.True(t, result, "Should be healthy when not processing regardless of time")
}

func TestJitterTime_ReturnsValueLessThanBase(t *testing.T) {
	base := 100 * time.Millisecond
	maxJitterPct := 0.20

	for range 100 {
		result := jitterTime(base, maxJitterPct)
		assert.LessOrEqual(t, result, base, "Jittered time should be <= base")
		assert.GreaterOrEqual(t, result, time.Duration(float64(base)*(1-maxJitterPct)), "Jittered time should be >= base*(1-maxJitterPct)")
	}
}

func TestJitterTime_ZeroJitter_ReturnsBase(t *testing.T) {
	base := 100 * time.Millisecond
	maxJitterPct := 0.0

	result := jitterTime(base, maxJitterPct)

	assert.Equal(t, base, result, "Zero jitter should return base unchanged")
}

func TestJitterTime_DifferentBaseDurations(t *testing.T) {
	tests := []struct {
		name         string
		base         time.Duration
		maxJitterPct float64
	}{
		{"1 second with 10% jitter", 1 * time.Second, 0.10},
		{"10 seconds with 20% jitter", 10 * time.Second, 0.20},
		{"1 minute with 5% jitter", 1 * time.Minute, 0.05},
		{"100ms with 50% jitter", 100 * time.Millisecond, 0.50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := jitterTime(tt.base, tt.maxJitterPct)

			minExpected := time.Duration(float64(tt.base) * (1 - tt.maxJitterPct))
			assert.LessOrEqual(t, result, tt.base)
			assert.GreaterOrEqual(t, result, minExpected)
		})
	}
}

func TestDisableSubsystemCausingError_DisablesALPN_OnALPNError(t *testing.T) {
	logger := zap.NewNop()
	sm := &streamManager{
		streamClient: &streamClient{
			tlsAuthProperties: tls.AuthProperties{
				DisableALPN: false,
				DisableTLS:  false,
			},
		},
	}

	sm.disableSubsystemCausingError(tls.ErrTLSALPNHandshakeFailed, logger)

	assert.True(t, sm.streamClient.tlsAuthProperties.DisableALPN)
	assert.False(t, sm.streamClient.tlsAuthProperties.DisableTLS)
	assert.False(t, sm.streamClient.disableNetworkFlowsCilium)
}

func TestDisableSubsystemCausingError_DisablesTLS_OnNoTLSError(t *testing.T) {
	logger := zap.NewNop()
	sm := &streamManager{
		streamClient: &streamClient{
			tlsAuthProperties: tls.AuthProperties{
				DisableALPN: false,
				DisableTLS:  false,
			},
		},
	}

	sm.disableSubsystemCausingError(tls.ErrNoTLSHandshakeFailed, logger)

	assert.False(t, sm.streamClient.tlsAuthProperties.DisableALPN)
	assert.True(t, sm.streamClient.tlsAuthProperties.DisableTLS)
	assert.False(t, sm.streamClient.disableNetworkFlowsCilium)
}

func TestDisableSubsystemCausingError_DisablesCilium_OnUnknownError(t *testing.T) {
	logger := zap.NewNop()
	sm := &streamManager{
		streamClient: &streamClient{
			tlsAuthProperties: tls.AuthProperties{
				DisableALPN: false,
				DisableTLS:  false,
			},
			disableNetworkFlowsCilium: false,
		},
	}

	sm.disableSubsystemCausingError(assert.AnError, logger)

	assert.False(t, sm.streamClient.tlsAuthProperties.DisableALPN)
	assert.False(t, sm.streamClient.tlsAuthProperties.DisableTLS)
	assert.True(t, sm.streamClient.disableNetworkFlowsCilium)
}

func TestStreamType_Constants(t *testing.T) {
	assert.Equal(t, STREAM_NETWORK_FLOWS, StreamType("network_flows"))
	assert.Equal(t, STREAM_RESOURCES, StreamType("resources"))
	assert.Equal(t, STREAM_LOGS, StreamType("logs"))
	assert.Equal(t, STREAM_CONFIGURATION, StreamType("configuration"))
}

func TestEnvironmentConfig_Defaults(t *testing.T) {
	// Test that EnvironmentConfig can be created with zero values
	cfg := EnvironmentConfig{}

	assert.Empty(t, cfg.CiliumNamespaces)
	assert.Empty(t, cfg.ClusterCreds)
	assert.False(t, cfg.TlsSkipVerify)
	assert.False(t, cfg.VerboseDebugging)
}

func TestKeepalivePeriods_ZeroValues(t *testing.T) {
	kp := KeepalivePeriods{}

	assert.Equal(t, time.Duration(0), kp.Configuration)
	assert.Equal(t, time.Duration(0), kp.KubernetesNetworkFlows)
	assert.Equal(t, time.Duration(0), kp.KubernetesResources)
	assert.Equal(t, time.Duration(0), kp.Logs)
}

func TestStreamSuccessPeriods_ZeroValues(t *testing.T) {
	sp := StreamSuccessPeriods{}

	assert.Equal(t, time.Duration(0), sp.Auth)
	assert.Equal(t, time.Duration(0), sp.Connect)
}

func TestStreamManager_WithInjectedK8sClient(t *testing.T) {
	sm := &streamManager{
		k8sClient: nil,
		clock:     NewRealClock(),
	}

	assert.Nil(t, sm.k8sClient)
	assert.NotNil(t, sm.clock)
}

func TestWatcherInfo_Fields(t *testing.T) {
	info := watcherInfo{
		resource:        "pods",
		apiGroup:        "",
		resourceVersion: "12345",
	}

	assert.Equal(t, "pods", info.resource)
	assert.Empty(t, info.apiGroup)
	assert.Equal(t, "12345", info.resourceVersion)
}

func TestStreamClient_Fields(t *testing.T) {
	falcoChan := make(chan string)
	sc := &streamClient{
		ciliumNamespaces:          []string{"kube-system"},
		ipfixCollectorPort:        "4739",
		disableNetworkFlowsCilium: false,
		falcoEventChan:            falcoChan,
	}

	assert.Equal(t, []string{"kube-system"}, sc.ciliumNamespaces)
	assert.Equal(t, "4739", sc.ipfixCollectorPort)
	assert.False(t, sc.disableNetworkFlowsCilium)
	assert.NotNil(t, sc.falcoEventChan)
}

func TestDeadlockDetector_ConcurrentAccess(t *testing.T) {
	// Reset global state
	dd.mutex.Lock()
	dd.processingResources = false
	dd.timeStarted = time.Time{}
	dd.mutex.Unlock()

	done := make(chan bool)

	// Start multiple goroutines accessing dd concurrently
	for range 10 {
		go func() {
			for range 100 {
				_ = ServerIsHealthy()
			}

			done <- true
		}()
	}

	// Start goroutines that modify dd
	for range 5 {
		go func() {
			for range 50 {
				dd.mutex.Lock()
				dd.processingResources = !dd.processingResources
				dd.timeStarted = time.Now()
				dd.mutex.Unlock()
			}

			done <- true
		}()
	}

	// Wait for all goroutines
	for range 15 {
		<-done
	}
}

func TestStreamManager_WithMockClock(t *testing.T) {
	mockClock := &testClock{
		now: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	sm := &streamManager{
		clock: mockClock,
	}

	assert.NotNil(t, sm.clock)
	assert.Equal(t, mockClock.now, sm.clock.Now())
}

// testClock is a simple mock clock for testing.
type testClock struct {
	now time.Time
}

func (c *testClock) Now() time.Time {
	return c.now
}

func (c *testClock) Since(t time.Time) time.Duration {
	return c.now.Sub(t)
}

func (c *testClock) NewTimer(d time.Duration) Timer {
	return &testTimer{c: make(chan time.Time, 1)}
}

func (c *testClock) NewTicker(d time.Duration) Ticker {
	return &testTicker{c: make(chan time.Time, 1)}
}

func (c *testClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- c.now.Add(d)

	return ch
}

type testTimer struct {
	c chan time.Time
}

func (t *testTimer) C() <-chan time.Time        { return t.c }
func (t *testTimer) Stop() bool                 { return true }
func (t *testTimer) Reset(d time.Duration) bool { return true }

type testTicker struct {
	c chan time.Time
}

func (t *testTicker) C() <-chan time.Time { return t.c }
func (t *testTicker) Stop()               {}

func TestRealClock_Implementation(t *testing.T) {
	clock := NewRealClock()

	// Test Now
	before := time.Now()
	now := clock.Now()
	after := time.Now()
	assert.True(t, !now.Before(before) && !now.After(after))

	// Test Since
	past := time.Now().Add(-1 * time.Hour)
	since := clock.Since(past)
	assert.GreaterOrEqual(t, since, 1*time.Hour)

	// Test NewTimer
	timer := clock.NewTimer(1 * time.Millisecond)
	assert.NotNil(t, timer)
	assert.NotNil(t, timer.C())

	// Test NewTicker
	ticker := clock.NewTicker(1 * time.Millisecond)
	assert.NotNil(t, ticker)
	assert.NotNil(t, ticker.C())
	ticker.Stop()

	// Test After
	ch := clock.After(1 * time.Millisecond)
	assert.NotNil(t, ch)
}

func TestRealTimer_StopAndReset(t *testing.T) {
	clock := NewRealClock()
	timer := clock.NewTimer(1 * time.Hour)

	// Stop should return true for an active timer
	stopped := timer.Stop()
	assert.True(t, stopped)

	// Reset after stop
	reset := timer.Reset(1 * time.Hour)
	assert.False(t, reset) // Timer was stopped, so Reset returns false
}

func TestStreamManager_FlowCache(t *testing.T) {
	flowCache := NewFlowCache(
		10*time.Second,
		100,
		make(chan pb.Flow, 10),
	)

	sm := &streamManager{
		FlowCache: flowCache,
	}

	assert.NotNil(t, sm.FlowCache)
}

func TestStreamManager_Stats(t *testing.T) {
	stats := NewStreamStats()

	sm := &streamManager{
		stats: stats,
	}

	assert.NotNil(t, sm.stats)

	// Test incrementing stats
	sm.stats.IncrementFlowsReceived()
	sm.stats.IncrementFlowsSentToClusterSync()
	sm.stats.IncrementResourceMutations()

	// Verify stats were incremented using GetAndResetStats
	flowsReceived, flowsSent, mutations := sm.stats.GetAndResetStats()
	assert.Equal(t, uint64(1), flowsReceived)
	assert.Equal(t, uint64(1), flowsSent)
	assert.Equal(t, uint64(1), mutations)
}

func TestFlowCache_Creation(t *testing.T) {
	outChan := make(chan pb.Flow, 10)
	fc := NewFlowCache(20*time.Second, 1000, outChan)

	assert.NotNil(t, fc)
	assert.Equal(t, 20*time.Second, fc.activeTimeout)
	assert.Equal(t, 1000, fc.maxFlows)
}

func TestStreamManager_WithFlowCache(t *testing.T) {
	outChan := make(chan pb.Flow, 10)
	fc := NewFlowCache(20*time.Second, 1000, outChan)

	sm := &streamManager{
		FlowCache: fc,
	}

	assert.NotNil(t, sm.FlowCache)
	assert.Equal(t, 20*time.Second, sm.FlowCache.activeTimeout)
}

func TestParsePodNetworkInfo_InvalidInput(t *testing.T) {
	// Test with empty input
	result, err := parsePodNetworkInfo("")
	assert.Nil(t, result)
	require.ErrorIs(t, err, ErrFalcoEventIsNotFlow)

	// Test with incomplete input
	result, err = parsePodNetworkInfo("incomplete data")
	assert.Nil(t, result)
	require.ErrorIs(t, err, ErrFalcoEventIsNotFlow)
}

func TestFilterIllumioTraffic(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"empty string", "", false},
		{"non-illumio traffic", "some random traffic", false},
		{"illumio traffic", "illumio_network_traffic event data", true},
		{"contains illumio_network_traffic", "prefix illumio_network_traffic suffix", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterIllumioTraffic(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestStreamClient_WithMockStreams(t *testing.T) {
	mockResource := &mockResourceStream{}
	mockFlows := &mockNetworkFlowsStream{}

	sc := &streamClient{
		resourceStream:     mockResource,
		networkFlowsStream: mockFlows,
	}

	assert.NotNil(t, sc.resourceStream)
	assert.NotNil(t, sc.networkFlowsStream)
}
