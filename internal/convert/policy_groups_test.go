// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/convert"
	"github.com/illumio/cloud-operator/internal/convert/awsvpccni"
	"github.com/illumio/cloud-operator/internal/convert/cilium"
)

// This test lives in an external package so it can import the converter
// packages, which import convert. The expected groups come from their APIGroup
// constants, so the group strings in convert cannot drift from them.
func TestExtractGroupResource(t *testing.T) {
	tests := []struct {
		name     string
		data     *pb.ConfiguredKubernetesObjectData
		expected schema.GroupResource
	}{
		{
			name: "CiliumNetworkPolicy",
			data: &pb.ConfiguredKubernetesObjectData{
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy{
					CiliumNetworkPolicy: &pb.KubernetesCiliumNetworkPolicyData{},
				},
			},
			expected: schema.GroupResource{Group: cilium.APIGroup, Resource: "ciliumnetworkpolicies"},
		},
		{
			name: "CiliumClusterwideNetworkPolicy",
			data: &pb.ConfiguredKubernetesObjectData{
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy{
					CiliumClusterwideNetworkPolicy: &pb.KubernetesCiliumClusterwideNetworkPolicyData{},
				},
			},
			expected: schema.GroupResource{Group: cilium.APIGroup, Resource: "ciliumclusterwidenetworkpolicies"},
		},
		{
			name: "CiliumCIDRGroup",
			data: &pb.ConfiguredKubernetesObjectData{
				KindSpecific: &pb.ConfiguredKubernetesObjectData_CiliumCidrGroup{
					CiliumCidrGroup: &pb.KubernetesCiliumCIDRGroupData{},
				},
			},
			expected: schema.GroupResource{Group: cilium.APIGroup, Resource: "ciliumcidrgroups"},
		},
		{
			name: "AWS ClusterNetworkPolicy",
			data: &pb.ConfiguredKubernetesObjectData{
				KindSpecific: &pb.ConfiguredKubernetesObjectData_AwsClusterNetworkPolicy{
					AwsClusterNetworkPolicy: &pb.KubernetesAWSClusterNetworkPolicyData{},
				},
			},
			expected: schema.GroupResource{Group: awsvpccni.APIGroup, Resource: "clusternetworkpolicies"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			groupResource, err := convert.ExtractGroupResource(tc.data)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, groupResource)
		})
	}
}

func TestExtractGroupResource_Unsupported(t *testing.T) {
	_, err := convert.ExtractGroupResource(&pb.ConfiguredKubernetesObjectData{})
	require.ErrorContains(t, err, "unsupported kind_specific")
}
