// Copyright 2026 Illumio, Inc. All Rights Reserved.

package falco

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/collector"
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

// FalcoEventHandlerTestSuite tests the Falco HTTP event handler directly.
type FalcoEventHandlerTestSuite struct {
	suite.Suite

	eventChan chan string
	handler   http.HandlerFunc
}

func TestFalcoEventHandlerTestSuite(t *testing.T) {
	suite.Run(t, new(FalcoEventHandlerTestSuite))
}

func (s *FalcoEventHandlerTestSuite) SetupTest() {
	s.eventChan = make(chan string, 1) // Buffered to avoid blocking
	s.handler = collector.NewFalcoEventHandler(s.eventChan)
}

func (s *FalcoEventHandlerTestSuite) TearDownTest() {
	close(s.eventChan)
}

func (s *FalcoEventHandlerTestSuite) TestHandler_ValidIllumioEvent() {
	event := map[string]string{
		"output": "illumio_network_traffic (time=2024-01-15T10:30:00.000000000-0500 srcip=10.0.0.1 dstip=10.0.0.2 srcport=12345 dstport=80 proto=tcp ipversion=ipv4)",
	}
	body, err := json.Marshal(event)
	s.Require().NoError(err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	s.handler(rec, req)

	s.Equal(http.StatusOK, rec.Code)

	// Verify the event was sent to the channel
	select {
	case received := <-s.eventChan:
		s.Contains(received, "illumio_network_traffic")
		s.Contains(received, "srcip=10.0.0.1")
		s.Contains(received, "dstip=10.0.0.2")
	case <-time.After(100 * time.Millisecond):
		s.Fail("expected event on channel")
	}
}

func (s *FalcoEventHandlerTestSuite) TestHandler_InvalidJSON() {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", bytes.NewReader([]byte("not json")))
	rec := httptest.NewRecorder()

	s.handler(rec, req)

	s.Equal(http.StatusBadRequest, rec.Code)
}

func (s *FalcoEventHandlerTestSuite) TestHandler_EmptyOutput() {
	event := map[string]string{
		"output": "",
	}
	body, err := json.Marshal(event)
	s.Require().NoError(err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	s.handler(rec, req)

	s.Equal(http.StatusOK, rec.Code)

	// Empty output should still be sent to channel
	select {
	case received := <-s.eventChan:
		s.Empty(received)
	case <-time.After(100 * time.Millisecond):
		s.Fail("expected event on channel")
	}
}

// FalcoFlowParsingTestSuite tests the flow parsing logic.
type FalcoFlowParsingTestSuite struct {
	suite.Suite
}

func TestFalcoFlowParsingTestSuite(t *testing.T) {
	suite.Run(t, new(FalcoFlowParsingTestSuite))
}

func (s *FalcoFlowParsingTestSuite) TestFilterIllumioTraffic_Match() {
	output := "illumio_network_traffic (time=2024-01-15T10:30:00.000000000-0500 srcip=10.0.0.1 dstip=10.0.0.2 srcport=12345 dstport=80 proto=tcp)"
	s.True(collector.FilterIllumioTraffic(output))
}

func (s *FalcoFlowParsingTestSuite) TestFilterIllumioTraffic_NoMatch() {
	output := "some_other_event (data=foo)"
	s.False(collector.FilterIllumioTraffic(output))
}

func (s *FalcoFlowParsingTestSuite) TestParsePodNetworkInfo_ValidTCP() {
	input := "time=2024-01-15T10:30:00.000000000-0500 srcip=192.168.1.10 dstip=192.168.1.20 srcport=45678 dstport=443 proto=tcp ipversion=ipv4"

	flow, err := collector.ParsePodNetworkInfo(input)

	s.Require().NoError(err)
	s.Require().NotNil(flow)
	s.NotNil(flow.GetLayer3())
	s.NotNil(flow.GetLayer4())
}

func (s *FalcoFlowParsingTestSuite) TestParsePodNetworkInfo_ValidUDP() {
	input := "time=2024-01-15T10:30:00.000000000-0500 srcip=10.0.0.5 dstip=10.0.0.10 srcport=53000 dstport=53 proto=udp ipversion=ipv4"

	flow, err := collector.ParsePodNetworkInfo(input)

	s.Require().NoError(err)
	s.Require().NotNil(flow)
}

func (s *FalcoFlowParsingTestSuite) TestParsePodNetworkInfo_EmptyInput() {
	flow, err := collector.ParsePodNetworkInfo("")

	s.Nil(flow)
	s.Error(err) // ErrFalcoEventIsNotFlow
}

func (s *FalcoFlowParsingTestSuite) TestParsePodNetworkInfo_InvalidPort() {
	input := "time=2024-01-15T10:30:00.000000000-0500 srcip=10.0.0.1 dstip=10.0.0.2 srcport=invalid dstport=80 proto=tcp ipversion=ipv4"

	flow, err := collector.ParsePodNetworkInfo(input)

	s.Nil(flow)
	s.Error(err) // ErrFalcoInvalidPort
}

func (s *FalcoFlowParsingTestSuite) TestParsePodNetworkInfo_InvalidTimestamp() {
	input := "time=not-a-timestamp srcip=10.0.0.1 dstip=10.0.0.2 srcport=12345 dstport=80 proto=tcp ipversion=ipv4"

	flow, err := collector.ParsePodNetworkInfo(input)

	s.Nil(flow)
	s.Error(err) // ErrFalcoTimestamp
}
