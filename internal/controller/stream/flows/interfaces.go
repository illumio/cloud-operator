// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
)

// Collector collects network flows.
type Collector interface {
	Run(ctx context.Context) error
}

// CollectorFactory creates flow collectors.
type CollectorFactory interface {
	NewCollector(ctx context.Context) (Collector, error)
}
