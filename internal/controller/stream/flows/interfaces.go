// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
	"time"
)

// Collector collects network flows.
type Collector interface {
	Run(ctx context.Context) error
}

// CollectorFactory creates flow collectors.
type CollectorFactory interface {
	NewCollector(ctx context.Context) (Collector, error)
	// PollingInterval returns the effective polling interval, or zero for a streaming collector.
	PollingInterval() time.Duration
}
