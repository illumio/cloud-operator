// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
)

// FlowCollector collects network flows.
type FlowCollector interface {
	Run(ctx context.Context) error
}

// FlowCollectorFactory is a function that creates flow collectors.
type FlowCollectorFactory func(ctx context.Context) (FlowCollector, error)
