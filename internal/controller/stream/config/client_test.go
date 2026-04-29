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
	"github.com/illumio/cloud-operator/internal/controller/stream/config/cache"
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
		cache:              cache.NewConfiguredObjectCache(),
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
	// Unknown outer response type causes the stream to close with an error.
	unknownResp := &pb.GetConfigurationUpdatesResponse{}
	s.mockStream.On("Recv").Return(unknownResp, nil).Once()

	err := s.client.Run(context.Background())

	s.Require().Error(err)
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

// Cache-related tests

func (s *ConfigClientTestSuite) TestRun_ResourceDataDuringSnapshot() {
	resourceDataResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "policy-1",
				Name: "allow-web",
			},
		},
	}
	s.mockStream.On("Recv").Return(resourceDataResp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())

	// Verify object was stored in cache
	obj, ok := s.client.cache.Get("policy-1")
	s.True(ok)
	s.Equal("allow-web", obj.GetName())
}

func (s *ConfigClientTestSuite) TestRun_ResourceDataAfterSnapshotComplete() {
	// First send snapshot complete
	snapshotCompleteResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	}
	// Then send resource data (should be ignored)
	resourceDataResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "policy-late",
				Name: "late-policy",
			},
		},
	}

	s.mockStream.On("Recv").Return(snapshotCompleteResp, nil).Once()
	s.mockStream.On("Recv").Return(resourceDataResp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())

	// Verify object was NOT stored (ResourceData after snapshot complete is ignored)
	_, ok := s.client.cache.Get("policy-late")
	s.False(ok)
}

func (s *ConfigClientTestSuite) TestRun_SnapshotComplete() {
	resourceDataResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "policy-1",
				Name: "allow-web",
			},
		},
	}
	snapshotCompleteResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	}

	s.mockStream.On("Recv").Return(resourceDataResp, nil).Once()
	s.mockStream.On("Recv").Return(snapshotCompleteResp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	s.False(s.client.cache.IsSnapshotComplete())

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())
	s.True(s.client.cache.IsSnapshotComplete())
	s.Equal(1, s.client.cache.Len())
}

func (s *ConfigClientTestSuite) TestRun_DuplicateSnapshotComplete() {
	snapshotCompleteResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	}
	s.mockStream.On("Recv").Return(snapshotCompleteResp, nil).Once()
	s.mockStream.On("Recv").Return(snapshotCompleteResp, nil).Once() // Duplicate
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())
	// Should still be complete (duplicate ignored, no error)
	s.True(s.client.cache.IsSnapshotComplete())
}

func (s *ConfigClientTestSuite) TestRun_MutationBeforeSnapshotComplete() {
	mutationResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_CreateObject{
					CreateObject: &pb.ConfiguredKubernetesObjectData{
						Id:   "policy-early",
						Name: "early-policy",
					},
				},
			},
		},
	}
	s.mockStream.On("Recv").Return(mutationResp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())

	// Verify mutation was ignored (before snapshot complete)
	_, ok := s.client.cache.Get("policy-early")
	s.False(ok)

	// Stats should NOT be incremented for ignored mutations
	_, _, _, configuredObjectMutations := s.stats.GetAndResetStats()
	s.Equal(uint64(0), configuredObjectMutations)
}

func (s *ConfigClientTestSuite) TestRun_MutationCreate() {
	// First complete the snapshot
	snapshotCompleteResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	}
	createMutationResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_CreateObject{
					CreateObject: &pb.ConfiguredKubernetesObjectData{
						Id:   "policy-new",
						Name: "new-policy",
					},
				},
			},
		},
	}

	s.mockStream.On("Recv").Return(snapshotCompleteResp, nil).Once()
	s.mockStream.On("Recv").Return(createMutationResp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())

	obj, ok := s.client.cache.Get("policy-new")
	s.True(ok)
	s.Equal("new-policy", obj.GetName())

	// Stats should be incremented
	_, _, _, configuredObjectMutations := s.stats.GetAndResetStats()
	s.Equal(uint64(1), configuredObjectMutations)
}

func (s *ConfigClientTestSuite) TestRun_MutationUpdate() {
	// Add initial object during snapshot
	resourceDataResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "policy-1",
				Name: "original-name",
			},
		},
	}
	snapshotCompleteResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	}
	updateMutationResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_UpdateObject{
					UpdateObject: &pb.ConfiguredKubernetesObjectData{
						Id:   "policy-1",
						Name: "updated-name",
					},
				},
			},
		},
	}

	s.mockStream.On("Recv").Return(resourceDataResp, nil).Once()
	s.mockStream.On("Recv").Return(snapshotCompleteResp, nil).Once()
	s.mockStream.On("Recv").Return(updateMutationResp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())

	obj, ok := s.client.cache.Get("policy-1")
	s.True(ok)
	s.Equal("updated-name", obj.GetName())

	// Stats should be incremented
	_, _, _, configuredObjectMutations := s.stats.GetAndResetStats()
	s.Equal(uint64(1), configuredObjectMutations)
}

func (s *ConfigClientTestSuite) TestRun_MutationDelete() {
	// Add initial object during snapshot
	resourceDataResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "policy-1",
				Name: "to-delete",
			},
		},
	}
	snapshotCompleteResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	}
	deleteMutationResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_DeleteObject{
					DeleteObject: &pb.DeleteConfiguredKubernetesObject{
						Id: "policy-1",
					},
				},
			},
		},
	}

	s.mockStream.On("Recv").Return(resourceDataResp, nil).Once()
	s.mockStream.On("Recv").Return(snapshotCompleteResp, nil).Once()
	s.mockStream.On("Recv").Return(deleteMutationResp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())

	_, ok := s.client.cache.Get("policy-1")
	s.False(ok)
	s.Equal(0, s.client.cache.Len())

	// Stats should be incremented
	_, _, _, configuredObjectMutations := s.stats.GetAndResetStats()
	s.Equal(uint64(1), configuredObjectMutations)
}

func (s *ConfigClientTestSuite) TestRun_FullSnapshotThenMutationsFlow() {
	// Snapshot phase
	resourceData1 := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{Id: "p1", Name: "policy-1"},
		},
	}
	resourceData2 := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{Id: "p2", Name: "policy-2"},
		},
	}
	snapshotComplete := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	}
	// Mutation phase
	createMutation := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_CreateObject{
					CreateObject: &pb.ConfiguredKubernetesObjectData{Id: "p3", Name: "policy-3"},
				},
			},
		},
	}
	updateMutation := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_UpdateObject{
					UpdateObject: &pb.ConfiguredKubernetesObjectData{Id: "p1", Name: "policy-1-updated"},
				},
			},
		},
	}
	deleteMutation := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_DeleteObject{
					DeleteObject: &pb.DeleteConfiguredKubernetesObject{Id: "p2"},
				},
			},
		},
	}

	s.mockStream.On("Recv").Return(resourceData1, nil).Once()
	s.mockStream.On("Recv").Return(resourceData2, nil).Once()
	s.mockStream.On("Recv").Return(snapshotComplete, nil).Once()
	s.mockStream.On("Recv").Return(createMutation, nil).Once()
	s.mockStream.On("Recv").Return(updateMutation, nil).Once()
	s.mockStream.On("Recv").Return(deleteMutation, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())

	// Verify final state
	s.True(s.client.cache.IsSnapshotComplete())
	s.Equal(2, s.client.cache.Len()) // p1 (updated) and p3, p2 was deleted

	obj1, ok := s.client.cache.Get("p1")
	s.True(ok)
	s.Equal("policy-1-updated", obj1.GetName())

	_, ok = s.client.cache.Get("p2")
	s.False(ok) // Deleted

	obj3, ok := s.client.cache.Get("p3")
	s.True(ok)
	s.Equal("policy-3", obj3.GetName())

	// Stats: 3 mutations (create, update, delete)
	_, _, _, configuredObjectMutations := s.stats.GetAndResetStats()
	s.Equal(uint64(3), configuredObjectMutations)
}

func (s *ConfigClientTestSuite) TestRun_EmptySnapshot() {
	// Snapshot with zero ResourceData messages
	snapshotCompleteResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	}

	s.mockStream.On("Recv").Return(snapshotCompleteResp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())

	// Snapshot complete with empty cache is valid
	s.True(s.client.cache.IsSnapshotComplete())
	s.Equal(0, s.client.cache.Len())
}

func (s *ConfigClientTestSuite) TestRun_UnknownMutationType() {
	snapshotCompleteResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	}
	// Mutation with nil inner type
	unknownMutationResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: nil, // Unknown/nil mutation type
			},
		},
	}

	s.mockStream.On("Recv").Return(snapshotCompleteResp, nil).Once()
	s.mockStream.On("Recv").Return(unknownMutationResp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	// Should not panic, just log warning
	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())

	// Stats still incremented (mutation was processed, just had unknown inner type)
	_, _, _, configuredObjectMutations := s.stats.GetAndResetStats()
	s.Equal(uint64(1), configuredObjectMutations)
}

func (s *ConfigClientTestSuite) TestRun_ResourceDataWithEmptyId() {
	resourceDataResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "", // Empty ID
				Name: "policy-with-empty-id",
			},
		},
	}

	s.mockStream.On("Recv").Return(resourceDataResp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())

	// Object stored with empty string key (edge case but valid Go behavior)
	obj, ok := s.client.cache.Get("")
	s.True(ok)
	s.Equal("policy-with-empty-id", obj.GetName())
}

func (s *ConfigClientTestSuite) TestRun_DeleteNonExistentObject() {
	snapshotCompleteResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	}
	// Delete an object that was never in the cache
	deleteMutationResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_DeleteObject{
					DeleteObject: &pb.DeleteConfiguredKubernetesObject{
						Id: "non-existent-id",
					},
				},
			},
		},
	}

	s.mockStream.On("Recv").Return(snapshotCompleteResp, nil).Once()
	s.mockStream.On("Recv").Return(deleteMutationResp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	// Should not panic or error - deleting non-existent is a no-op
	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())
	s.Equal(0, s.client.cache.Len())

	// Stats still incremented
	_, _, _, configuredObjectMutations := s.stats.GetAndResetStats()
	s.Equal(uint64(1), configuredObjectMutations)
}

func (s *ConfigClientTestSuite) TestRun_UpdateConfigurationDuringSnapshotPhase() {
	// Verify UpdateConfiguration works during snapshot phase (before SnapshotComplete)
	configResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_DEBUG,
			},
		},
	}
	resourceDataResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "policy-1",
				Name: "test-policy",
			},
		},
	}

	s.mockStream.On("Recv").Return(configResp, nil).Once()
	s.mockStream.On("Recv").Return(resourceDataResp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())

	// Both should have been processed
	s.Equal(zap.DebugLevel, s.syncer.GetLogLevel())
	s.Equal(1, s.client.cache.Len())
}

func (s *ConfigClientTestSuite) TestRun_UpdateConfigurationDuringMutationPhase() {
	// Verify UpdateConfiguration works during mutation phase (after SnapshotComplete)
	snapshotCompleteResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	}
	configResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_UpdateConfiguration{
			UpdateConfiguration: &pb.GetConfigurationUpdatesResponse_Configuration{
				LogLevel: pb.LogLevel_LOG_LEVEL_WARN,
			},
		},
	}
	createMutationResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceMutation{
			ResourceMutation: &pb.ConfiguredKubernetesObjectMutation{
				Mutation: &pb.ConfiguredKubernetesObjectMutation_CreateObject{
					CreateObject: &pb.ConfiguredKubernetesObjectData{
						Id:   "policy-1",
						Name: "test-policy",
					},
				},
			},
		},
	}

	s.mockStream.On("Recv").Return(snapshotCompleteResp, nil).Once()
	s.mockStream.On("Recv").Return(configResp, nil).Once()
	s.mockStream.On("Recv").Return(createMutationResp, nil).Once()
	s.mockStream.On("Recv").Return(nil, io.EOF).Once()

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())

	// Both should have been processed
	s.Equal(zap.WarnLevel, s.syncer.GetLogLevel())
	s.Equal(1, s.client.cache.Len())

	// Stats should be incremented for the mutation
	_, _, _, configuredObjectMutations := s.stats.GetAndResetStats()
	s.Equal(uint64(1), configuredObjectMutations)
}
