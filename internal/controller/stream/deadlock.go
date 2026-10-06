// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	"fmt"
	"sync"
	"time"
)

type deadlockDetector struct {
	mutex                 sync.RWMutex
	processingResources   bool
	timeStarted           time.Time
	flowInactivityTimeout time.Duration
	flowCollectionStarted time.Time
	lastFlowSent          time.Time
}

var dd = &deadlockDetector{}

// SetProcessingResources updates the deadlock detector state.
func SetProcessingResources(processing bool) {
	dd.mutex.Lock()
	defer dd.mutex.Unlock()

	dd.processingResources = processing
	if processing {
		dd.timeStarted = time.Now()
	}
}

// FlowInactivityTimeout allows several keepalive periods for background traffic,
// a collector polling interval, cache residence, and a margin for delivery.
func FlowInactivityTimeout(activeTimeout, pollInterval time.Duration, keepalivePeriods ...time.Duration) time.Duration {
	var minKeepalivePeriod time.Duration
	for _, period := range keepalivePeriods {
		if period > 0 && (minKeepalivePeriod == 0 || period < minKeepalivePeriod) {
			minKeepalivePeriod = period
		}
	}

	return FlowInactivityKeepaliveMultiplier*minKeepalivePeriod + max(pollInterval, 0) + activeTimeout + FlowInactivityMargin
}

// ConfigureFlowLiveness configures the flow watchdog once at startup. A
// nonpositive timeout disables it. Collection must start before the timer runs.
func ConfigureFlowLiveness(timeout time.Duration) {
	dd.mutex.Lock()
	defer dd.mutex.Unlock()

	dd.flowInactivityTimeout = timeout
	dd.flowCollectionStarted = time.Time{}
	dd.lastFlowSent = time.Time{}
}

// StartFlowCollection starts the grace period for the first flow. Subsequent
// collector or stream restarts must not reset the timer and hide stalled flows.
func StartFlowCollection() {
	dd.mutex.Lock()
	defer dd.mutex.Unlock()

	if dd.flowInactivityTimeout > 0 && dd.flowCollectionStarted.IsZero() {
		dd.flowCollectionStarted = time.Now()
	}
}

// RecordFlowSent records a successful data-flow send. Keepalives and failed
// sends do not demonstrate that flow collection and delivery are working.
func RecordFlowSent() {
	dd.mutex.Lock()
	defer dd.mutex.Unlock()

	if !dd.flowCollectionStarted.IsZero() {
		dd.lastFlowSent = time.Now()
	}
}

// ServerIsHealthy checks whether resource processing or flow delivery has stalled.
func ServerIsHealthy() bool {
	return UnhealthyReason() == ""
}

// UnhealthyReason returns why the server is unhealthy, or an empty string if it is healthy.
func UnhealthyReason() string {
	dd.mutex.RLock()
	defer dd.mutex.RUnlock()

	if dd.processingResources {
		if elapsed := time.Since(dd.timeStarted); elapsed > ResourceProcessingTimeout {
			return fmt.Sprintf("resource processing has been running for %s", elapsed.Round(time.Second))
		}
	}

	if !dd.flowCollectionStarted.IsZero() {
		lastActivity := dd.lastFlowSent
		if lastActivity.IsZero() {
			lastActivity = dd.flowCollectionStarted
		}

		if elapsed := time.Since(lastActivity); elapsed > dd.flowInactivityTimeout {
			return fmt.Sprintf("no network flow has been sent for %s", elapsed.Round(time.Second))
		}
	}

	return ""
}
