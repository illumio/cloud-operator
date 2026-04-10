// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"

	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// FlowCollector is an alias for stream.FlowCollector.
type FlowCollector = stream.FlowCollector

// FlowCollectorFactory creates flow collectors.
type FlowCollectorFactory interface {
	NewFlowCollector(ctx context.Context) (stream.FlowCollector, error)
}
