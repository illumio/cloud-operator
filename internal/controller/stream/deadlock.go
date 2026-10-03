// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	"fmt"
	"sync"
	"time"
)

type deadlockDetector struct {
	mutex               sync.RWMutex
	processingResources bool
	timeStarted         time.Time
	sendingFlow         bool
	flowSendStarted     time.Time
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

// SetSendingFlow updates the deadlock detector state for a Send on the network flows stream.
func SetSendingFlow(sending bool) {
	dd.mutex.Lock()
	defer dd.mutex.Unlock()

	dd.sendingFlow = sending
	if sending {
		dd.flowSendStarted = time.Now()
	}
}

// ServerIsHealthy checks if a deadlock has occurred within the resource listing
// process or while sending a network flow.
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

	if dd.sendingFlow {
		if elapsed := time.Since(dd.flowSendStarted); elapsed > FlowSendTimeout {
			return fmt.Sprintf("send on network flows stream has been blocked for %s", elapsed.Round(time.Second))
		}
	}

	return ""
}
