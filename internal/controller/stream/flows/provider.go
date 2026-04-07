// Copyright 2026 Illumio, Inc. All Rights Reserved.

package flows

import (
	"context"
	"sync"

	"google.golang.org/grpc"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// Verify FlowCollectorProvider implements stream.StreamClientFactory.
var _ stream.StreamClientFactory = (*FlowCollectorProvider)(nil)

// FlowCollectorProvider encapsulates flow collector determination and factory creation.
// It implements StreamClientFactory so manager can treat it as a regular factory.
// It also provides GetFlowCollectorType() for resources factory to query.
type FlowCollectorProvider struct {
	config FlowCollectorConfig

	mu            sync.Mutex
	sessionCtx    context.Context
	collectorType pb.FlowCollector
	innerFactory  stream.StreamClientFactory
}

// NewFlowCollectorProvider creates a new FlowCollectorProvider with the given config.
func NewFlowCollectorProvider(config FlowCollectorConfig) *FlowCollectorProvider {
	return &FlowCollectorProvider{
		config:        config,
		collectorType: pb.FlowCollector_FLOW_COLLECTOR_UNSPECIFIED,
	}
}

// NewStreamClient determines the flow collector (if not already determined for this session)
// and delegates to the appropriate factory.
func (p *FlowCollectorProvider) NewStreamClient(ctx context.Context, conn grpc.ClientConnInterface) (stream.StreamClient, error) {
	p.mu.Lock()
	// New context = new session = re-determine
	if p.sessionCtx != ctx {
		p.sessionCtx = ctx
		p.collectorType, p.innerFactory = DetermineFlowCollector(ctx, p.config)
	}
	factory := p.innerFactory
	p.mu.Unlock()

	if factory == nil {
		// No flow collector available - return a no-op client
		return &noOpFlowCollectorClient{}, nil
	}

	return factory.NewStreamClient(ctx, conn)
}

// Name returns the stream name for logging.
func (p *FlowCollectorProvider) Name() string {
	return "FlowCollector"
}

// GetFlowCollectorType returns the determined flow collector type.
// Returns UNSPECIFIED if not yet determined.
func (p *FlowCollectorProvider) GetFlowCollectorType() pb.FlowCollector {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.collectorType
}

// noOpFlowCollectorClient is a no-op client used when no flow collector is available.
type noOpFlowCollectorClient struct{}

func (c *noOpFlowCollectorClient) Run(ctx context.Context) error {
	// Block until context is canceled
	<-ctx.Done()

	return ctx.Err()
}

func (c *noOpFlowCollectorClient) SendKeepalive(_ context.Context) error {
	return nil
}

func (c *noOpFlowCollectorClient) Close() error {
	return nil
}
