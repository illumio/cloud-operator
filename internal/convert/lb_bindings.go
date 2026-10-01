// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// API groups of the provider objects that bind a load balancer to a Service.
// Conversion matches on the group as well as the kind, so an unrelated CRD that
// shares a kind name is not parsed with the wrong schema.
const (
	awsLoadBalancerControllerGroup = "elbv2.k8s.aws"
	gkeNetworkingGroup             = "networking.gke.io"
	metalLBGroup                   = "metallb.io"
)

// Labels GKE sets on a ServiceNetworkEndpointGroup to name its Service.
// https://github.com/kubernetes/ingress-gce/blob/master/pkg/neg/types/types.go
const (
	gkeNegServiceNameLabel = "networking.gke.io/service-name"
	gkeNegServicePortLabel = "networking.gke.io/service-port"
)

// convertLoadBalancerBinding sets KindSpecific for the provider objects that
// describe how a load balancer reaches a Service: AWS TargetGroupBinding, GKE
// ServiceNetworkEndpointGroup and MetalLB ServiceL2Status/ServiceBGPStatus.
// Other objects are left unchanged.
func convertLoadBalancerBinding(objMetadata *pb.KubernetesObjectData, obj *unstructured.Unstructured, kind, apiGroup string) error {
	if obj == nil {
		return nil
	}

	switch {
	case kind == "TargetGroupBinding" && apiGroup == awsLoadBalancerControllerGroup:
		var tgb tgbTargetGroupBinding
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &tgb); err != nil {
			return fmt.Errorf("deserializing %s: %w", kind, err)
		}

		objMetadata.KindSpecific = &pb.KubernetesObjectData_TargetGroupBinding{
			TargetGroupBinding: convertTargetGroupBinding(&tgb),
		}
	case kind == "ServiceNetworkEndpointGroup" && apiGroup == gkeNetworkingGroup:
		var svcNeg svcNegServiceNetworkEndpointGroup
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &svcNeg); err != nil {
			return fmt.Errorf("deserializing %s: %w", kind, err)
		}

		objMetadata.KindSpecific = &pb.KubernetesObjectData_ServiceNetworkEndpointGroup{
			ServiceNetworkEndpointGroup: convertServiceNetworkEndpointGroup(&svcNeg, obj.GetLabels()),
		}
	case (kind == "ServiceL2Status" || kind == "ServiceBGPStatus") && apiGroup == metalLBGroup:
		var status metalLBServiceStatus
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &status); err != nil {
			return fmt.Errorf("deserializing %s: %w", kind, err)
		}

		objMetadata.KindSpecific = &pb.KubernetesObjectData_MetallbServiceStatus{
			MetallbServiceStatus: convertMetalLBServiceStatus(&status),
		}
	}

	return nil
}

func convertTargetGroupBinding(tgb *tgbTargetGroupBinding) *pb.KubernetesTargetGroupBindingData {
	spec := tgb.Spec

	return &pb.KubernetesTargetGroupBindingData{
		ServiceName:             spec.ServiceRef.Name,
		ServicePort:             spec.ServiceRef.Port.String(),
		TargetType:              nonEmptyString(spec.TargetType),
		TargetGroupArn:          spec.TargetGroupARN,
		TargetGroupName:         nonEmptyString(&spec.TargetGroupName),
		IpAddressType:           nonEmptyString(spec.IPAddressType),
		NodeSelector:            convertLabelSelectorToProto(spec.NodeSelector),
		NetworkingIngressRules:  convertTGBIngressRules(spec.Networking),
		VpcId:                   nonEmptyString(&spec.VpcID),
		TargetGroupProtocol:     nonEmptyString(spec.TargetGroupProtocol),
		MultiClusterTargetGroup: spec.MultiClusterTargetGroup,
	}
}

func convertTGBIngressRules(networking *tgbNetworking) []*pb.TargetGroupBindingIngressRule {
	if networking == nil || len(networking.Ingress) == 0 {
		return nil
	}

	out := make([]*pb.TargetGroupBindingIngressRule, 0, len(networking.Ingress))
	for _, rule := range networking.Ingress {
		out = append(out, &pb.TargetGroupBindingIngressRule{
			From:  convertTGBPeers(rule.From),
			Ports: convertTGBPorts(rule.Ports),
		})
	}

	return out
}

func convertTGBPeers(peers []tgbPeer) []*pb.TargetGroupBindingPeer {
	if len(peers) == 0 {
		return nil
	}

	out := make([]*pb.TargetGroupBindingPeer, 0, len(peers))
	for _, peer := range peers {
		pbPeer := &pb.TargetGroupBindingPeer{}

		if peer.IPBlock != nil {
			pbPeer.Cidr = nonEmptyString(&peer.IPBlock.CIDR)
		}

		if peer.SecurityGroup != nil {
			pbPeer.SecurityGroupId = nonEmptyString(&peer.SecurityGroup.GroupID)
		}

		out = append(out, pbPeer)
	}

	return out
}

func convertTGBPorts(ports []tgbPort) []*pb.TargetGroupBindingPort {
	if len(ports) == 0 {
		return nil
	}

	out := make([]*pb.TargetGroupBindingPort, 0, len(ports))
	for _, port := range ports {
		pbPort := &pb.TargetGroupBindingPort{
			Protocol: nonEmptyString(port.Protocol),
		}

		if port.Port != nil {
			portValue := port.Port.String()
			pbPort.Port = &portValue
		}

		out = append(out, pbPort)
	}

	return out
}

func convertServiceNetworkEndpointGroup(svcNeg *svcNegServiceNetworkEndpointGroup, labels map[string]string) *pb.KubernetesServiceNetworkEndpointGroupData {
	out := &pb.KubernetesServiceNetworkEndpointGroupData{
		ServiceName: labels[gkeNegServiceNameLabel],
		ServicePort: labels[gkeNegServicePortLabel],
	}

	for _, neg := range svcNeg.Status.NetworkEndpointGroups {
		out.NetworkEndpointGroups = append(out.NetworkEndpointGroups, &pb.KubernetesServiceNetworkEndpointGroupData_NetworkEndpointGroup{
			Id:                  neg.ID,
			NetworkEndpointType: neg.NetworkEndpointType,
			SelfLink:            neg.SelfLink,
			SubnetUrl:           neg.SubnetURL,
			State:               neg.State,
		})
	}

	return out
}

func convertMetalLBServiceStatus(status *metalLBServiceStatus) *pb.KubernetesMetalLBServiceStatusData {
	out := &pb.KubernetesMetalLBServiceStatusData{
		ServiceName:      status.Status.ServiceName,
		ServiceNamespace: status.Status.ServiceNamespace,
		Node:             status.Status.Node,
		Peers:            status.Status.Peers,
	}

	for _, iface := range status.Status.Interfaces {
		if iface.Name != "" {
			out.Interfaces = append(out.Interfaces, iface.Name)
		}
	}

	return out
}

// nonEmptyString returns s, or nil when s is nil or empty, so empty CRD fields
// stay unset in the proto.
func nonEmptyString(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}

	return s
}
