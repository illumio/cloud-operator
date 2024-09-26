package controller

import (
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8scluster/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/watch"
)

func (suite *ControllerTestSuite) TestStreamMutationObjectMetaData() {
	tests := map[string]struct {
		eventType    watch.EventType
		metadata     *pb.KubernetesObjectMetadata
		expectedCall *pb.SendKubernetesResourcesRequest
		expectedErr  error
	}{
		"Added event": {
			eventType: watch.Added,
			metadata:  &pb.KubernetesObjectMetadata{Name: "test"},
			expectedCall: &pb.SendKubernetesResourcesRequest{
				Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{
					KubernetesResourceMutation: &pb.KubernetesResourceMutation{
						Mutation: &pb.KubernetesResourceMutation_CreateResource{
							CreateResource: &pb.KubernetesObjectMetadata{Name: "test"},
						},
					},
				},
			},
			expectedErr: nil,
		},
		"Deleted event": {
			eventType: watch.Deleted,
			metadata:  &pb.KubernetesObjectMetadata{Name: "test"},
			expectedCall: &pb.SendKubernetesResourcesRequest{
				Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{
					KubernetesResourceMutation: &pb.KubernetesResourceMutation{
						Mutation: &pb.KubernetesResourceMutation_DeleteResource{
							DeleteResource: &pb.KubernetesObjectMetadata{Name: "test"},
						},
					},
				},
			},
			expectedErr: nil,
		},
		"Modified event": {
			eventType: watch.Modified,
			metadata:  &pb.KubernetesObjectMetadata{Name: "test"},
			expectedCall: &pb.SendKubernetesResourcesRequest{
				Request: &pb.SendKubernetesResourcesRequest_KubernetesResourceMutation{
					KubernetesResourceMutation: &pb.KubernetesResourceMutation{
						Mutation: &pb.KubernetesResourceMutation_UpdateResource{
							UpdateResource: &pb.KubernetesObjectMetadata{Name: "test"},
						},
					},
				},
			},
			expectedErr: nil,
		},
	}
	sm := &streamManager{
		logger: suite.logger,
		instance: &streamClient{
			
		}
	}

	for name, tc := range tests {
		suite.Run(name, func() {
			// Set up the mock expectations
			

			// Call the function under test
			err := streamMutationObjectMetaData(MockStreamManager, tc.metadata, tc.eventType)

			// Assert the results
			if tc.expectedErr != nil {
				assert.Error(suite.T(), err)
			} else {
				assert.NoError(suite.T(), err)
			}

			// Assert that the mock expectations were met
			suite.MockResourceStream.AssertExpectations(suite.T())
		})
	}
}
