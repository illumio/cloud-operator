// Copyright 2026 Illumio, Inc. All Rights Reserved.

package stream

import "time"

// HTTP Server Configuration.
const (
	// HTTPReadHeaderTimeout is the timeout for reading request headers.
	HTTPReadHeaderTimeout = 5 * time.Second
	// HTTPReadTimeout is the timeout for reading the entire request.
	HTTPReadTimeout = 5 * time.Second
	// ServerRestartDelay is the delay before restarting the Falco server after failure.
	ServerRestartDelay = 5 * time.Second
)

// Connection Retry Configuration.
const (
	// ConnectionRetryInterval is the initial interval between connection attempts.
	ConnectionRetryInterval = 5 * time.Second
	// ConnectionRetryAfterFailure is the interval after a failed connection attempt.
	ConnectionRetryAfterFailure = 10 * time.Second
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

// Flow Cache Configuration.
const (
	// FlowCacheActiveTimeout is the timeout for active flows in the cache.
	FlowCacheActiveTimeout = 20 * time.Second
	// FlowCacheMaxSize is the maximum number of flows to cache.
	FlowCacheMaxSize = 1000
	// FlowChannelBufferSize is the buffer size for flow channels.
	FlowChannelBufferSize = 100
)
