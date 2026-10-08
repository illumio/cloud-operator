// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// entryPointFieldNumbers are the kind_specific fields added for Ingress and the
// Gateway API.
var entryPointFieldNumbers = []int32{117, 118, 119, 120, 121}

// oldKubernetesObjectDataDescriptor returns KubernetesObjectData as a receiver
// built before the entry point fields existed sees it: the current schema with
// those fields removed.
func oldKubernetesObjectDataDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	file := protodesc.ToFileDescriptorProto(pb.File_illumio_cloud_k8sclustersync_v1_k8s_info_proto)

	var found bool

	for _, message := range file.GetMessageType() {
		if message.GetName() != "KubernetesObjectData" {
			continue
		}

		message.Field = slices.DeleteFunc(message.GetField(), func(field *descriptorpb.FieldDescriptorProto) bool {
			return slices.Contains(entryPointFieldNumbers, field.GetNumber())
		})
		found = true
	}

	require.True(t, found, "KubernetesObjectData not found in the file descriptor")

	oldFile, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	descriptor := oldFile.Messages().ByName("KubernetesObjectData")
	require.NotNil(t, descriptor)

	return descriptor
}

// A CloudSecure build that predates the entry point fields must still decode
// the object's metadata, and see no kind_specific, exactly as it did before the
// operator started sending them.
func TestEntryPointFields_DecodedByOldReceiver(t *testing.T) {
	oldDescriptor := oldKubernetesObjectDataDescriptor(t)

	tests := map[string]*pb.KubernetesObjectData{
		"Ingress": {
			KindSpecific: &pb.KubernetesObjectData_Ingress{Ingress: &pb.KubernetesIngressData{
				IngressClassName: new("nginx"),
				Rules: []*pb.KubernetesIngressData_Rule{{
					Host: new("shop.example.com"),
					Paths: []*pb.KubernetesIngressData_Path{{
						PathType: "Prefix",
						Backend:  &pb.KubernetesIngressData_Backend{ServiceName: new("web"), ServicePortNumber: new(uint32(80))},
					}},
				}},
				LoadBalancerIngress: []*pb.LoadBalancerIngress{{Ip: new("203.0.113.10")}},
			}},
		},
		"IngressClass": {
			KindSpecific: &pb.KubernetesObjectData_IngressClass{IngressClass: &pb.KubernetesIngressClassData{Controller: "k8s.io/ingress-nginx"}},
		},
		"Gateway": {
			KindSpecific: &pb.KubernetesObjectData_Gateway{Gateway: &pb.KubernetesGatewayData{
				GatewayClassName: "eg",
				Listeners:        []*pb.KubernetesGatewayData_Listener{{Name: "http", Port: 80, Protocol: "HTTP"}},
			}},
		},
		"GatewayClass": {
			KindSpecific: &pb.KubernetesObjectData_GatewayClass{GatewayClass: &pb.KubernetesGatewayClassData{ControllerName: "example.com/controller"}},
		},
		"HTTPRoute": {
			KindSpecific: &pb.KubernetesObjectData_GatewayRoute{GatewayRoute: &pb.KubernetesGatewayRouteData{
				ParentRefs:  []*pb.KubernetesGatewayRouteData_ParentReference{{Name: "edge"}},
				BackendRefs: []*pb.KubernetesGatewayRouteData_BackendRef{{Name: "web", Port: new(uint32(80))}},
			}},
		},
	}

	for kind, sent := range tests {
		t.Run(kind, func(t *testing.T) {
			sent.Kind = kind
			sent.Name = "edge"
			sent.Namespace = new("default")
			sent.Uid = "uid-1"
			sent.ResourceVersion = "42"
			sent.ApiGroup = "gateway.networking.k8s.io"
			sent.ApiVersion = "v1"
			sent.Labels = map[string]string{"app": "shop"}

			wire, err := proto.Marshal(sent)
			require.NoError(t, err)

			received := dynamicpb.NewMessage(oldDescriptor)
			require.NoError(t, proto.Unmarshal(wire, received))

			fields := oldDescriptor.Fields()
			assert.Equal(t, kind, received.Get(fields.ByName("kind")).String())
			assert.Equal(t, "edge", received.Get(fields.ByName("name")).String())
			assert.Equal(t, "default", received.Get(fields.ByName("namespace")).String())
			assert.Equal(t, "uid-1", received.Get(fields.ByName("uid")).String())
			assert.Equal(t, "42", received.Get(fields.ByName("resource_version")).String())
			assert.Equal(t, "gateway.networking.k8s.io", received.Get(fields.ByName("api_group")).String())
			assert.Equal(t, "v1", received.Get(fields.ByName("api_version")).String())
			assert.Equal(t, "shop", received.Get(fields.ByName("labels")).Map().Get(protoreflect.ValueOfString("app").MapKey()).String())

			// The new field is unknown to the old receiver: kind_specific is unset.
			oneof := oldDescriptor.Oneofs().ByName("kind_specific")
			assert.Nil(t, received.WhichOneof(oneof))
			assert.NotEmpty(t, received.GetUnknown(), "the new field is kept as unknown bytes, not misread as another field")
		})
	}
}
