// Copyright 2026 Illumio, Inc. All Rights Reserved.

// Package timeutil provides time-related utility functions.
package timeutil

import (
	"math/rand"
	"time"
)

// JitterTime subtracts a random percentage from the base time to introduce jitter.
// maxJitterPct must be in the range [0, 1).
// This is useful for preventing thundering herd problems when multiple clients
// reconnect or send keepalives at the same time.
func JitterTime(base time.Duration, maxJitterPct float64) time.Duration {
	jitterPct := rand.Float64() * maxJitterPct //nolint:gosec

	return time.Duration(float64(base) * (1. - jitterPct))
}
