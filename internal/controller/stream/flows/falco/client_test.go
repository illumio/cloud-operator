// Copyright 2026 Illumio, Inc. All Rights Reserved.

package falco

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// mockFlowSink mocks the collector.FlowSink interface.
type mockFlowSink struct {
	mock.Mock
}

func (m *mockFlowSink) CacheFlow(ctx context.Context, flow pb.Flow) error {
	args := m.Called(ctx, flow)

	return args.Error(0)
}

func (m *mockFlowSink) IncrementFlowsReceived() {
	m.Called()
}

// FalcoClientTestSuite tests the falcoClient.
// Note: Since Run() now creates its own channel and starts its own HTTP server,
// we can only test context cancellation. Integration tests would be needed for
// full flow testing via HTTP requests.
type FalcoClientTestSuite struct {
	suite.Suite

	logger   *zap.Logger
	client   *falcoClient
	mockSink *mockFlowSink
}

func TestFalcoClientTestSuite(t *testing.T) {
	suite.Run(t, new(FalcoClientTestSuite))
}

func (s *FalcoClientTestSuite) SetupTest() {
	s.logger = zap.NewNop()
	s.mockSink = &mockFlowSink{}
	s.client = &falcoClient{
		logger:   s.logger,
		flowSink: s.mockSink,
	}
}

func (s *FalcoClientTestSuite) TestRun_ContextCanceled() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := s.client.Run(ctx)

	s.Require().ErrorIs(err, context.Canceled)
}
