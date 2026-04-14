// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import "time"

// Connection Retry Configuration.
const (
	// ConnectionRetryInterval is the initial interval between connection attempts.
	ConnectionRetryInterval = 5 * time.Second
	// ConnectionRetryJitter is the jitter percentage for connection retry timers.
	ConnectionRetryJitter = 0.20
)

// Stream Backoff Configuration.
const (
	// StreamInitialBackoff is the initial backoff duration for stream reconnection.
	StreamInitialBackoff = 1 * time.Second
	// StreamMaxBackoff is the maximum backoff duration for stream reconnection.
	StreamMaxBackoff = 1 * time.Minute
	// StreamMaxJitterPct is the maximum jitter percentage for stream backoff.
	StreamMaxJitterPct = 0.20
	// StreamSevereErrorThreshold is the number of errors before considering severe.
	StreamSevereErrorThreshold = 10
	// StreamExponentialFactor is the exponential factor for backoff calculation.
	StreamExponentialFactor = 2.0
)

// Reset Backoff Configuration.
const (
	// ResetInitialBackoff is the initial backoff for reset operations.
	ResetInitialBackoff = 10 * time.Minute
	// ResetMaxBackoff is the maximum backoff for reset operations.
	ResetMaxBackoff = 10 * time.Second
	// ResetMaxJitterPct is the maximum jitter percentage for reset backoff.
	ResetMaxJitterPct = 0.10
)

// Health Check Configuration.
const (
	// ResourceProcessingTimeout is the maximum time allowed for resource processing
	// before the server is considered unhealthy.
	ResourceProcessingTimeout = 5 * time.Minute
)
