// Copyright 2026 Illumio, Inc. All Rights Reserved.

package ovnk

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

// OVNKClientTestSuite tests the ovnkClient.
// Note: Full integration tests sending IPFIX messages require the template system
// and binary templates from /ipfix-template-sets/openvswitch.bin.
// Unit tests for IPFIX parsing are in internal/controller/collector/ovnk_test.go.
type OVNKClientTestSuite struct {
	suite.Suite

	logger   *zap.Logger
	client   *ovnkClient
	mockSink *mockFlowSink
}

func TestOVNKClientTestSuite(t *testing.T) {
	suite.Run(t, new(OVNKClientTestSuite))
}

func (s *OVNKClientTestSuite) SetupTest() {
	s.logger = zap.NewNop()
	s.mockSink = &mockFlowSink{}
	s.client = &ovnkClient{
		logger:             s.logger,
		ipfixCollectorPort: "0", // Use port 0 to get an available port
		flowSink:           s.mockSink,
	}
}

func (s *OVNKClientTestSuite) TestClient_Fields() {
	s.Equal(s.logger, s.client.logger)
	s.Equal("0", s.client.ipfixCollectorPort)
	s.Equal(s.mockSink, s.client.flowSink)
}
