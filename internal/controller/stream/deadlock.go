// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	"sync"
	"time"
)

type deadlockDetector struct {
	mutex               sync.RWMutex
	processingResources bool
	timeStarted         time.Time
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

// ServerIsHealthy checks if a deadlock has occurred within the resource listing process.
func ServerIsHealthy() bool {
	dd.mutex.RLock()
	defer dd.mutex.RUnlock()

	if dd.processingResources && time.Since(dd.timeStarted) > ResourceProcessingTimeout {
		return false
	}

	return true
}
