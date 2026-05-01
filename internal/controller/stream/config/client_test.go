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

// isReady is a test helper to check if the cache's IsReady channel is closed.
func isReady(c *cache.ConfiguredObjectCache) bool {
	select {
	case <-c.IsReady():
		return true
	default:
		return false
	}
}

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

// Cache-related tests.

func (s *ConfigClientTestSuite) TestRun_FirstSnapshotFails_CacheStaysEmpty() {
	// Send partial data, then error (no SnapshotComplete)
	resourceDataResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "policy-1",
				Name: "partial-data",
			},
		},
	}

	s.mockStream.On("Recv").Return(resourceDataResp, nil).Once()
	s.mockStream.On("Recv").Return(nil, errors.New("connection lost")).Once()

	err := s.client.Run(context.Background())

	s.Require().Error(err)
	s.Equal(0, s.client.cache.Len()) // Cache stays empty
	s.False(isReady(s.client.cache)) // Channel still open (not ready)
}

func (s *ConfigClientTestSuite) TestRun_ReconnectionFails_CacheKeepsOldData() {
	// Pre-populate cache with "old" data (simulating previous successful snapshot)
	s.client.cache.ReplaceAll(map[string]*pb.ConfiguredKubernetesObjectData{
		"old-policy": {Id: "old-policy", Name: "old-data"},
	})

	// Now simulate reconnection that fails partway through new snapshot
	resourceDataResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "new-policy",
				Name: "new-partial-data",
			},
		},
	}

	s.mockStream.On("Recv").Return(resourceDataResp, nil).Once()
	s.mockStream.On("Recv").Return(nil, errors.New("connection lost")).Once()

	err := s.client.Run(context.Background())

	s.Require().Error(err)
	s.Equal(1, s.client.cache.Len())           // Still has old data count
	s.NotNil(s.client.cache.Get("old-policy")) // Old data preserved
	s.Nil(s.client.cache.Get("new-policy"))    // New partial data NOT in cache
	s.True(isReady(s.client.cache))            // Still ready (was ready before)
}

func (s *ConfigClientTestSuite) TestRun_ReconnectionAcceptsNewSnapshot() {
	// Simulate first successful snapshot
	s.client.cache.ReplaceAll(map[string]*pb.ConfiguredKubernetesObjectData{
		"old-policy": {Id: "old-policy", Name: "old-data"},
	})
	s.True(isReady(s.client.cache)) // Cache is ready from "previous" stream

	// Now simulate a NEW stream (reconnection) with new snapshot
	// Even though cache is ready, THIS stream should accept ResourceData
	resourceDataResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceData{
			ResourceData: &pb.ConfiguredKubernetesObjectData{
				Id:   "new-policy",
				Name: "new-data",
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

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	// New snapshot should have replaced old data
	s.Equal(1, s.client.cache.Len())
	s.Nil(s.client.cache.Get("old-policy"))    // Old data gone
	s.NotNil(s.client.cache.Get("new-policy")) // New data present
	s.Equal("new-data", s.client.cache.Get("new-policy").GetName())
}

func (s *ConfigClientTestSuite) TestRun_ResourceDataDuringSnapshot() {
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

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())

	// Verify object was stored in cache after snapshot complete
	obj := s.client.cache.Get("policy-1")
	s.NotNil(obj)
	s.Equal("allow-web", obj.GetName())
}

func (s *ConfigClientTestSuite) TestRun_ResourceDataAfterSnapshotComplete() {
	// First send snapshot complete
	snapshotCompleteResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	}
	// Then send resource data (protocol violation - should error)
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

	err := s.client.Run(context.Background())

	s.Require().Error(err)
	s.Contains(err.Error(), "server sent ResourceData after snapshot complete")
	s.mockStream.AssertExpectations(s.T())

	// Verify object was NOT stored
	s.Nil(s.client.cache.Get("policy-late"))
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

	s.False(isReady(s.client.cache))

	err := s.client.Run(context.Background())

	s.Require().NoError(err)
	s.mockStream.AssertExpectations(s.T())
	s.True(isReady(s.client.cache))
	s.Equal(1, s.client.cache.Len())
}

func (s *ConfigClientTestSuite) TestRun_DuplicateSnapshotComplete() {
	snapshotCompleteResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	}
	s.mockStream.On("Recv").Return(snapshotCompleteResp, nil).Once()
	s.mockStream.On("Recv").Return(snapshotCompleteResp, nil).Once() // Duplicate - protocol violation

	err := s.client.Run(context.Background())

	s.Require().Error(err)
	// Should error - stream restarts
	s.Contains(err.Error(), "server sent duplicate ResourceSnapshotComplete")
	s.mockStream.AssertExpectations(s.T())
	// Should still be ready from first snapshot complete
	s.True(isReady(s.client.cache))
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

	err := s.client.Run(context.Background())

	s.Require().Error(err)
	// Should error - stream restarts
	s.Contains(err.Error(), "server sent ResourceMutation before snapshot complete")
	s.mockStream.AssertExpectations(s.T())

	// Verify mutation was NOT applied
	s.Nil(s.client.cache.Get("policy-early"))
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

	obj := s.client.cache.Get("policy-new")
	s.NotNil(obj)
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

	obj := s.client.cache.Get("policy-1")
	s.NotNil(obj)
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

	s.Nil(s.client.cache.Get("policy-1"))
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
	s.True(isReady(s.client.cache))
	s.Equal(2, s.client.cache.Len()) // p1 (updated) and p3, p2 was deleted

	obj1 := s.client.cache.Get("p1")
	s.NotNil(obj1)
	s.Equal("policy-1-updated", obj1.GetName())

	s.Nil(s.client.cache.Get("p2")) // Deleted

	obj3 := s.client.cache.Get("p3")
	s.NotNil(obj3)
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
	s.True(isReady(s.client.cache))
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

	err := s.client.Run(context.Background())

	// Unknown mutation type should close the stream with an error
	s.Require().Error(err)
	s.Contains(err.Error(), "unknown configured object mutation type")
	s.mockStream.AssertExpectations(s.T())

	// Stats should NOT be incremented (error occurred before increment)
	_, _, _, configuredObjectMutations := s.stats.GetAndResetStats()
	s.Equal(uint64(0), configuredObjectMutations)
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
	snapshotCompleteResp := &pb.GetConfigurationUpdatesResponse{
		Response: &pb.GetConfigurationUpdatesResponse_ResourceSnapshotComplete{
			ResourceSnapshotComplete: &pb.ConfiguredKubernetesObjectSnapshotComplete{},
		},
	}

	s.mockStream.On("Recv").Return(configResp, nil).Once()
	s.mockStream.On("Recv").Return(resourceDataResp, nil).Once()
	s.mockStream.On("Recv").Return(snapshotCompleteResp, nil).Once()
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
