// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	"time"
)

// ResourceProcessingTimeout is the maximum time allowed for resource processing before
// the server is considered unhealthy.
const ResourceProcessingTimeout = 5 * time.Minute

// ServerIsHealthy checks if a deadlock has occurred within the resource listing process.
func ServerIsHealthy() bool {
	dd.mutex.RLock()
	defer dd.mutex.RUnlock()

	if dd.processingResources && time.Since(dd.timeStarted) > ResourceProcessingTimeout {
		return false
	}

	return true
}
