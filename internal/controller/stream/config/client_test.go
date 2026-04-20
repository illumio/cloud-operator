// Copyright 2026 Illumio, Inc. All Rights Reserved.

package config

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/logging"
	"github.com/illumio/cloud-operator/internal/controller/stream"
)

// mockConfigurationStream mocks the stream.ConfigurationStream interface.
type mockConfigurationStream struct {
	mock.Mock
}

func (m *mockConfigurationStream) Send(req *pb.GetConfigurationUpdatesRequest) error {
	args := m.Called(req)

	return args.Error(0)
}

func (m *mockConfigurationStream) Recv() (*pb.GetConfigurationUpdatesResponse, error) {
	args := m.Called()
	if resp, ok := args.Get(0).(*pb.GetConfigurationUpdatesResponse); ok {
		return resp, args.Error(1)
	}

	return nil, args.Error(1)
}

// ConfigClientTestSuite tests the configClient.
type ConfigClientTestSuite struct {
	suite.Suite

	mockStream *mockConfigurationStream
	syncer     *logging.BufferedGrpcWriteSyncer
	logger     *zap.Logger
	stats      *stream.Stats
	client     *configClient
}

func TestConfigClientTestSuite(t *testing.T) {
	suite.Run(t, new(ConfigClientTestSuite))
}

func (s *ConfigClientTestSuite) SetupTest() {
	s.mockStream = &mockConfigurationStream{}
	s.logger = zap.NewNop()
	s.syncer = logging.NewBufferedGrpcWriteSyncerForTest(s.logger)
	s.stats = stream.NewStats()
	s.client = &configClient{
		stream:             s.mockStream,
		logger:             s.logger,
		bufferedGrpcSyncer: s.syncer,
		stats:              s.stats,
	}
}

func (s *ConfigClientTestSuite) TestRun_ContextCanceled() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := s.client.Run(ctx)

	s.Require().ErrorIs(err, context.Canceled)
}

func (s *ConfigClientTestSuite) TestRun_EOF() {
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())
}

func (s *ConfigClientTestSuite) TestRun_StreamError() {
	expectedErr := errors.New("stream error")
	s.mockStream.On("Recv").Return(nil, expectedErr).Once()

	err := s.client.Run(context.Background())

	s.Require().ErrorIs(err, expectedErr)
	s.mockStream.AssertExpectations(s.T())
}

func (s *ConfigClientTestSuite) TestRun_ConfigUpdate() {
	configResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_DEBUG,
			},
		},
	}
	s.mockStream.On("Recv").Return(configResp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())
	s.Equal(zap.DebugLevel, s.syncer.GetLogLevel(), "log level should be set to DEBUG")
}

func (s *ConfigClientTestSuite) TestRun_VerboseDebuggingOverride() {
	s.client.verboseDebugging = true

	// Config update with INFO level, but verboseDebugging should force DEBUG
	configResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_INFO,
			},
		},
	}
	s.mockStream.On("Recv").Return(configResp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())
}

func (s *ConfigClientTestSuite) TestRun_UnknownResponse() {
	// Unknown response type (nil inner response)
	unknownResp := &pb.GetConfigurationUpdatesResponse{}
	s.mockStream.On("Recv").Return(unknownResp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())
}

func (s *ConfigClientTestSuite) TestSendKeepalive_Success() {
	s.mockStream.On("Send", mock.MatchedBy(func(req *pb.GetConfigurationUpdatesRequest) bool {
		return req.GetKeepalive() != nil
	})).Return(nil).Once()

	err := s.client.SendKeepalive(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())
}

func (s *ConfigClientTestSuite) TestSendKeepalive_Error() {
	expectedErr := errors.New("send error")
	s.mockStream.On("Send", mock.Anything).Return(expectedErr).Once()

	err := s.client.SendKeepalive(context.Background())

	s.Require().ErrorIs(err, expectedErr)
	s.mockStream.AssertExpectations(s.T())
}

func (s *ConfigClientTestSuite) TestSendKeepalive_AfterClose() {
	// Close the client first
	err := s.client.Close()
	s.Require().NoError(err)

	// SendKeepalive should return error
	err = s.client.SendKeepalive(context.Background())

	s.Require().Error(err)
	s.Contains(err.Error(), "stream closed")
}

func (s *ConfigClientTestSuite) TestClose() {
	err := s.client.Close()

	s.Require().NoError(err)
	s.True(s.client.closed)
}

func (s *ConfigClientTestSuite) TestClose_Idempotent() {
	// Close multiple times should not error
	err := s.client.Close()
	s.Require().NoError(err)

	err = s.client.Close()
	s.Require().NoError(err)

	s.True(s.client.closed)
}

func (s *ConfigClientTestSuite) TestRun_NetworkPolicyMutation_Create() {
	resp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_NetworkPolicyMutation{
			NetworkPolicyMutation: &pb.NetworkPolicyMutation{
				Mutation: &pb.NetworkPolicyMutation_CreatePolicy{
					CreatePolicy: &pb.ConfiguredNetworkPolicyData{
						Id:   "policy-123",
						Name: "test-policy",
					},
				},
			},
		},
	}
	s.mockStream.On("Recv").Return(resp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())
	_, _, _, policyMutations := s.stats.GetAndResetStats()
	s.Equal(uint64(1), policyMutations, "policy mutations should be incremented")
}

func (s *ConfigClientTestSuite) TestRun_NetworkPolicyMutation_Update() {
	resp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_NetworkPolicyMutation{
			NetworkPolicyMutation: &pb.NetworkPolicyMutation{
				Mutation: &pb.NetworkPolicyMutation_UpdatePolicy{
					UpdatePolicy: &pb.ConfiguredNetworkPolicyData{
						Id:   "policy-123",
						Name: "test-policy",
					},
				},
			},
		},
	}
	s.mockStream.On("Recv").Return(resp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())
	_, _, _, policyMutations := s.stats.GetAndResetStats()
	s.Equal(uint64(1), policyMutations, "policy mutations should be incremented")
}

func (s *ConfigClientTestSuite) TestRun_NetworkPolicyMutation_Delete() {
	resp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_NetworkPolicyMutation{
			NetworkPolicyMutation: &pb.NetworkPolicyMutation{
				Mutation: &pb.NetworkPolicyMutation_DeletePolicy{
					DeletePolicy: &pb.DeleteNetworkPolicy{
						Id: "policy-123",
					},
				},
			},
		},
	}
	s.mockStream.On("Recv").Return(resp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())
	_, _, _, policyMutations := s.stats.GetAndResetStats()
	s.Equal(uint64(1), policyMutations, "policy mutations should be incremented")
}
