// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import (
	"errors"
	"time"
)

// Config holds configuration for stream manager authentication.
type Config struct {
	ClusterCreds           string
	ClusterName            string // Optional: cluster name for self-managed clusters
	ClusterRegion          string // Optional: cluster region for self-managed clusters
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

// ErrStopRetries signals that retries should stop.
var ErrStopRetries = errors.New("stop retries")
