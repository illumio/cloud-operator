// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// convertBinding runs an unstructured object through NewCoreResourceConverter,
// the converter the resource watcher uses.
func convertBinding(t *testing.T, obj map[string]any) *pb.KubernetesObjectData {
	t.Helper()

	return convertBindingWithLogger(t, obj, zap.NewNop())
}

func convertBindingWithLogger(t *testing.T, obj map[string]any, logger *zap.Logger) *pb.KubernetesObjectData {
	t.Helper()

	converter := NewCoreResourceConverter(k8sfake.NewSimpleClientset(), logger)

	result, err := converter(context.Background(), &unstructured.Unstructured{Object: obj})
	require.NoError(t, err)

	return result
}

func TestConvertTargetGroupBinding(t *testing.T) {
	tests := map[string]struct {
		apiVersion string
		spec       map[string]any
		expected   *pb.KubernetesTargetGroupBindingData
	}{
		"ip target type with numeric port and networking rules": {
			apiVersion: "elbv2.k8s.aws/v1beta1",
			spec: map[string]any{
				"serviceRef":          map[string]any{"name": "web", "port": int64(80)},
				"targetType":          "ip",
				"targetGroupARN":      "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/k8s-default-web/0123",
				"ipAddressType":       "ipv4",
				"vpcID":               "vpc-0123",
				"targetGroupProtocol": "TCP",
				"networking": map[string]any{
					"ingress": []any{
						map[string]any{
							"from": []any{
								map[string]any{"ipBlock": map[string]any{"cidr": "0.0.0.0/0"}},
								map[string]any{"securityGroup": map[string]any{"groupID": "sg-0123"}},
							},
							"ports": []any{
								map[string]any{"protocol": "TCP", "port": int64(8080)},
								map[string]any{"protocol": "TCP"},
							},
						},
					},
				},
			},
			expected: &pb.KubernetesTargetGroupBindingData{
				ServiceName:         "web",
				ServicePort:         "80",
				TargetType:          new("ip"),
				TargetGroupArn:      "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/k8s-default-web/0123",
				IpAddressType:       new("ipv4"),
				VpcId:               new("vpc-0123"),
				TargetGroupProtocol: new("TCP"),
				NetworkingIngressRules: []*pb.TargetGroupBindingIngressRule{{
					From: []*pb.TargetGroupBindingPeer{
						{Cidr: new("0.0.0.0/0")},
						{SecurityGroupId: new("sg-0123")},
					},
					Ports: []*pb.TargetGroupBindingPort{
						{Protocol: new("TCP"), Port: new("8080")},
						{Protocol: new("TCP")},
					},
				}},
			},
		},
		"instance target type with named port and node selector": {
			apiVersion: "elbv2.k8s.aws/v1beta1",
			spec: map[string]any{
				"serviceRef":              map[string]any{"name": "api", "port": "https"},
				"targetType":              "instance",
				"targetGroupName":         "shared-tg",
				"multiClusterTargetGroup": true,
				"nodeSelector": map[string]any{
					"matchLabels": map[string]any{"role": "edge"},
				},
			},
			expected: &pb.KubernetesTargetGroupBindingData{
				ServiceName:             "api",
				ServicePort:             "https",
				TargetType:              new("instance"),
				TargetGroupName:         new("shared-tg"),
				MultiClusterTargetGroup: true,
				NodeSelector:            &pb.LabelSelector{MatchLabels: map[string]string{"role": "edge"}},
			},
		},
		// v1alpha1 has no defaulting webhook, so targetType can be absent.
		"missing target type stays unset": {
			apiVersion: "elbv2.k8s.aws/v1alpha1",
			spec: map[string]any{
				"serviceRef":     map[string]any{"name": "web", "port": int64(80)},
				"targetGroupARN": "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/web/0123",
			},
			expected: &pb.KubernetesTargetGroupBindingData{
				ServiceName:    "web",
				ServicePort:    "80",
				TargetGroupArn: "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/web/0123",
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := convertBinding(t, map[string]any{
				"apiVersion": tt.apiVersion,
				"kind":       "TargetGroupBinding",
				"metadata":   map[string]any{"name": "tgb", "namespace": "default"},
				"spec":       tt.spec,
			})

			assert.Equal(t, tt.expected, result.GetTargetGroupBinding())
		})
	}
}

func TestConvertTargetGroupBinding_Malformed(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)

	result := convertBindingWithLogger(t, map[string]any{
		"apiVersion": "elbv2.k8s.aws/v1beta1",
		"kind":       "TargetGroupBinding",
		"metadata":   map[string]any{"name": "tgb", "namespace": "default"},
		"spec": map[string]any{
			"serviceRef": map[string]any{"name": "web", "port": true},
		},
	}, zap.New(core))

	// A malformed object is still sent, with metadata only, and the reason is logged.
	assert.Equal(t, "tgb", result.GetName())
	assert.Nil(t, result.GetKindSpecific())

	entries := logs.FilterMessage("Failed to read load balancer binding, sending metadata only").All()
	require.Len(t, entries, 1)
	assert.Equal(t, "TargetGroupBinding", entries[0].ContextMap()["kind"])
	assert.Equal(t, "default", entries[0].ContextMap()["namespace"])
	assert.Equal(t, "tgb", entries[0].ContextMap()["name"])
}

func TestConvertServiceNetworkEndpointGroup(t *testing.T) {
	tests := map[string]struct {
		labels   map[string]any
		status   map[string]any
		expected *pb.KubernetesServiceNetworkEndpointGroupData
	}{
		"pod-direct and node NEGs": {
			labels: map[string]any{
				"networking.gke.io/service-name": "web",
				"networking.gke.io/service-port": "80",
				"networking.gke.io/managed-by":   "neg-controller",
			},
			status: map[string]any{
				"networkEndpointGroups": []any{
					map[string]any{
						"id":                  "1234",
						"networkEndpointType": "GCE_VM_IP_PORT",
						"selfLink":            "https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a/networkEndpointGroups/k8s1-web",
						"subnetURL":           "https://www.googleapis.com/compute/v1/projects/p/regions/us-central1/subnetworks/default",
						"state":               "ACTIVE",
					},
					map[string]any{
						"id":                  "5678",
						"networkEndpointType": "GCE_VM_IP",
					},
				},
			},
			expected: &pb.KubernetesServiceNetworkEndpointGroupData{
				ServiceName: "web",
				ServicePort: "80",
				NetworkEndpointGroups: []*pb.KubernetesServiceNetworkEndpointGroupData_NetworkEndpointGroup{
					{
						Id:                  "1234",
						NetworkEndpointType: "GCE_VM_IP_PORT",
						SelfLink:            "https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a/networkEndpointGroups/k8s1-web",
						SubnetUrl:           "https://www.googleapis.com/compute/v1/projects/p/regions/us-central1/subnetworks/default",
						State:               "ACTIVE",
					},
					{Id: "5678", NetworkEndpointType: "GCE_VM_IP"},
				},
			},
		},
		"no labels and no status yet": {
			expected: &pb.KubernetesServiceNetworkEndpointGroupData{},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			metadata := map[string]any{"name": "k8s1-web", "namespace": "default"}
			if tt.labels != nil {
				metadata["labels"] = tt.labels
			}

			obj := map[string]any{
				"apiVersion": "networking.gke.io/v1beta1",
				"kind":       "ServiceNetworkEndpointGroup",
				"metadata":   metadata,
			}
			if tt.status != nil {
				obj["status"] = tt.status
			}

			result := convertBinding(t, obj)

			assert.Equal(t, tt.expected, result.GetServiceNetworkEndpointGroup())
		})
	}
}

func TestConvertMetalLBServiceStatus(t *testing.T) {
	tests := map[string]struct {
		kind     string
		status   map[string]any
		expected *pb.KubernetesMetalLBServiceStatusData
	}{
		"L2 status names the announcing node and interfaces": {
			kind: "ServiceL2Status",
			status: map[string]any{
				"node":             "worker-1",
				"serviceName":      "web",
				"serviceNamespace": "default",
				"interfaces":       []any{map[string]any{"name": "eth0"}, map[string]any{"name": "eth1"}},
			},
			expected: &pb.KubernetesMetalLBServiceStatusData{
				ServiceName:      "web",
				ServiceNamespace: "default",
				Node:             "worker-1",
				Interfaces:       []string{"eth0", "eth1"},
			},
		},
		"BGP status lists the peers": {
			kind: "ServiceBGPStatus",
			status: map[string]any{
				"node":             "worker-2",
				"serviceName":      "web",
				"serviceNamespace": "default",
				"peers":            []any{"tor-a", "tor-b"},
			},
			expected: &pb.KubernetesMetalLBServiceStatusData{
				ServiceName:      "web",
				ServiceNamespace: "default",
				Node:             "worker-2",
				Peers:            []string{"tor-a", "tor-b"},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := convertBinding(t, map[string]any{
				"apiVersion": "metallb.io/v1beta1",
				"kind":       tt.kind,
				"metadata":   map[string]any{"name": "l2-abcde", "namespace": "metallb-system"},
				"status":     tt.status,
			})

			assert.Equal(t, tt.expected, result.GetMetallbServiceStatus())
		})
	}
}

// A kind name alone must not select a converter: another CRD with the same kind
// in a different group is sent with metadata only.
func TestConvertLoadBalancerBinding_WrongGroup(t *testing.T) {
	for _, apiVersion := range []string{"example.com/v1", "v1"} {
		for _, kind := range []string{"TargetGroupBinding", "ServiceNetworkEndpointGroup", "ServiceL2Status", "ServiceBGPStatus"} {
			result := convertBinding(t, map[string]any{
				"apiVersion": apiVersion,
				"kind":       kind,
				"metadata":   map[string]any{"name": "x", "namespace": "default"},
				"spec":       map[string]any{"serviceRef": map[string]any{"name": "web", "port": int64(80)}},
				"status":     map[string]any{"node": "worker-1"},
			})

			require.NotNil(t, result)
			assert.Nil(t, result.GetKindSpecific(), "%s %s", apiVersion, kind)
		}
	}
}
