// Copyright 2026 Illumio, Inc. All Rights Reserved.

package falco

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// FalcoClientTestSuite tests the falcoClient.
type FalcoClientTestSuite struct {
	suite.Suite

	flowCache      *stream.FlowCache
	stats          *stream.Stats
	falcoEventChan chan string
	logger         *zap.Logger
	client         *falcoClient
	outFlows       chan pb.Flow
}

func TestFalcoClientTestSuite(t *testing.T) {
	suite.Run(t, new(FalcoClientTestSuite))
}

func (s *FalcoClientTestSuite) SetupTest() {
	s.logger = zap.NewNop()
	s.stats = stream.NewStats()
	s.outFlows = make(chan pb.Flow, 10)
	s.flowCache = stream.NewFlowCache(10*time.Second, 100, s.outFlows)
	s.falcoEventChan = make(chan string, 10)
	s.client = &falcoClient{
		logger:         s.logger,
		flowCache:      s.flowCache,
		stats:          s.stats,
		falcoEventChan: s.falcoEventChan,
	}
}

func (s *FalcoClientTestSuite) TearDownTest() {
	_ = s.flowCache.Close()
}

func (s *FalcoClientTestSuite) TestRun_ContextCanceled() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := s.client.Run(ctx)

	s.Require().ErrorIs(err, context.Canceled)
}

func (s *FalcoClientTestSuite) TestRun_ChannelClosed() {
	close(s.falcoEventChan)

	err := s.client.Run(context.Background())

	// Should exit gracefully when channel is closed
	s.Require().NoError(err)
}

func (s *FalcoClientTestSuite) TestRun_FilteredTraffic() {
	ctx, cancel := context.WithCancel(context.Background())

	// Send non-Illumio traffic (should be filtered)
	go func() {
		s.falcoEventChan <- "some random traffic that doesn't match Illumio pattern"

		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := s.client.Run(ctx)

	s.Require().ErrorIs(err, context.Canceled)
}

func (s *FalcoClientTestSuite) TestRun_InvalidFlowFormat() {
	ctx, cancel := context.WithCancel(context.Background())

	// Send Illumio traffic but with invalid format (no match for regex)
	go func() {
		s.falcoEventChan <- "illumio_traffic_event_without_parentheses"

		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := s.client.Run(ctx)

	// Should continue (not crash) on invalid format
	s.Require().ErrorIs(err, context.Canceled)
}

func (s *FalcoClientTestSuite) TestSendKeepalive_NoOp() {
	// SendKeepalive for Falco is a no-op (not a gRPC stream)
	err := s.client.SendKeepalive(context.Background())

	s.Require().NoError(err)
}

func (s *FalcoClientTestSuite) TestClose() {
	err := s.client.Close()

	s.Require().NoError(err)
}

func (s *FalcoClientTestSuite) TestClose_Idempotent() {
	err := s.client.Close()
	s.Require().NoError(err)

	err = s.client.Close()
	s.Require().NoError(err)
}

func (s *FalcoClientTestSuite) TestRun_ValidFlow() {
	ctx, cancel := context.WithCancel(context.Background())

	// Valid Falco event with all required fields
	validEvent := "illumio_network_traffic (srcip=10.0.0.1 dstip=10.0.0.2 srcport=12345 dstport=80 proto=tcp ipversion=ipv4 time=2024-01-01T00:00:00.000000000+0000)"

	go func() {
		s.falcoEventChan <- validEvent

		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := s.client.Run(ctx)

	s.Require().ErrorIs(err, context.Canceled)
	// Verify stats were incremented
	flowsReceived, _, _ := s.stats.GetAndResetStats()
	s.Equal(uint64(1), flowsReceived)
}

func (s *FalcoClientTestSuite) TestRun_EmptyParentheses() {
	ctx, cancel := context.WithCancel(context.Background())

	// Illumio traffic with empty parentheses (regex matches but ParsePodNetworkInfo returns nil)
	go func() {
		s.falcoEventChan <- "illumio_network_traffic ()"

		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := s.client.Run(ctx)

	// Should continue (ParsePodNetworkInfo returns nil for empty input)
	s.Require().ErrorIs(err, context.Canceled)
}

func (s *FalcoClientTestSuite) TestRun_PartialFlow() {
	ctx, cancel := context.WithCancel(context.Background())

	// Illumio traffic with partial data (missing required fields)
	go func() {
		s.falcoEventChan <- "illumio_network_traffic (srcip=10.0.0.1)"

		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := s.client.Run(ctx)

	// Should return error from ParsePodNetworkInfo (invalid port)
	s.Require().Error(err)
}
