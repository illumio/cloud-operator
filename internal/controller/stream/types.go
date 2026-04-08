// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	"errors"
	"sync"
	"time"
)

// SuccessPeriods defines how long a stream must be active to be considered successful.
type SuccessPeriods struct {
	Auth    time.Duration
	Connect time.Duration
}

// EnvironmentConfig holds configuration for stream manager authentication.
type EnvironmentConfig struct {
	ClusterCreds           string
	HttpsProxy             string
	OnboardingClientID     string
	OnboardingClientSecret string
	OnboardingEndpoint     string
	PodNamespace           string
	StatsLogPeriod         time.Duration
	SuccessPeriods         SuccessPeriods
	TlsSkipVerify          bool
	TokenEndpoint          string
}

type deadlockDetector struct {
	mutex               sync.RWMutex
	processingResources bool
	timeStarted         time.Time
}

var dd = &deadlockDetector{}

// ErrStopRetries signals that retries should stop.
var ErrStopRetries = errors.New("stop retries")

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
