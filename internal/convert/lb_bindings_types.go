// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// Local structs mirroring the provider CRDs that bind a load balancer to a
// Service. We convert the unstructured object into these plain structs with
// runtime.DefaultUnstructuredConverter and then convert to proto. Only the fields we
// send are mirrored, so no provider SDK is pulled in.
//
// Schema:
// - https://github.com/kubernetes-sigs/aws-load-balancer-controller/blob/main/apis/elbv2/v1beta1/targetgroupbinding_types.go
// - https://github.com/kubernetes/ingress-gce/blob/master/pkg/apis/svcneg/v1beta1/types.go
// - https://github.com/metallb/metallb/blob/main/api/v1beta1/servicel2status_types.go
// - https://github.com/metallb/metallb/blob/main/api/v1beta1/servicebgpstatus_types.go

// tgbTargetGroupBinding mirrors the AWS Load Balancer Controller TargetGroupBinding
// CRD top-level object (elbv2.k8s.aws/v1alpha1 and v1beta1).
type tgbTargetGroupBinding struct {
	Spec tgbSpec `json:"spec"`
}

// tgbSpec mirrors TargetGroupBindingSpec.
type tgbSpec struct {
	TargetGroupARN          string                `json:"targetGroupARN,omitempty"`
	TargetGroupName         string                `json:"targetGroupName,omitempty"`
	MultiClusterTargetGroup bool                  `json:"multiClusterTargetGroup,omitempty"`
	TargetType              *string               `json:"targetType,omitempty"`
	TargetGroupProtocol     *string               `json:"targetGroupProtocol,omitempty"`
	ServiceRef              tgbServiceReference   `json:"serviceRef"`
	Networking              *tgbNetworking        `json:"networking,omitempty"`
	NodeSelector            *metav1.LabelSelector `json:"nodeSelector,omitempty"`
	IPAddressType           *string               `json:"ipAddressType,omitempty"`
	VpcID                   string                `json:"vpcID,omitempty"`
}

// tgbServiceReference mirrors ServiceReference.
type tgbServiceReference struct {
	Name string             `json:"name"`
	Port intstr.IntOrString `json:"port"`
}

// tgbNetworking mirrors TargetGroupBindingNetworking.
type tgbNetworking struct {
	Ingress []tgbIngressRule `json:"ingress,omitempty"`
}

// tgbIngressRule mirrors NetworkingIngressRule.
type tgbIngressRule struct {
	From  []tgbPeer `json:"from"`
	Ports []tgbPort `json:"ports"`
}

// tgbPeer mirrors NetworkingPeer. Exactly one of IPBlock or SecurityGroup is set.
type tgbPeer struct {
	IPBlock       *tgbIPBlock       `json:"ipBlock,omitempty"`
	SecurityGroup *tgbSecurityGroup `json:"securityGroup,omitempty"`
}

// tgbIPBlock mirrors IPBlock.
type tgbIPBlock struct {
	CIDR string `json:"cidr"`
}

// tgbSecurityGroup mirrors SecurityGroup.
type tgbSecurityGroup struct {
	GroupID string `json:"groupID"`
}

// tgbPort mirrors NetworkingPort. A nil Port means all ports.
type tgbPort struct {
	Protocol *string             `json:"protocol,omitempty"`
	Port     *intstr.IntOrString `json:"port,omitempty"`
}

// svcNegServiceNetworkEndpointGroup mirrors the GKE ServiceNetworkEndpointGroup
// CRD top-level object (networking.gke.io/v1beta1). Its spec is empty: the
// Service is named by labels and the NEGs are reported in status.
type svcNegServiceNetworkEndpointGroup struct {
	Status svcNegStatus `json:"status"`
}

// svcNegStatus mirrors ServiceNetworkEndpointGroupStatus.
type svcNegStatus struct {
	NetworkEndpointGroups []svcNegObjectReference `json:"networkEndpointGroups,omitempty"`
}

// svcNegObjectReference mirrors NegObjectReference.
type svcNegObjectReference struct {
	ID                  string `json:"id"`
	SelfLink            string `json:"selfLink,omitempty"`
	SubnetURL           string `json:"subnetURL,omitempty"`
	NetworkEndpointType string `json:"networkEndpointType,omitempty"`
	State               string `json:"state,omitempty"`
}

// metalLBServiceStatus mirrors the MetalLB ServiceL2Status and ServiceBGPStatus
// CRD top-level objects (metallb.io/v1beta1), which differ only in status.
type metalLBServiceStatus struct {
	Status metalLBServiceStatusStatus `json:"status"`
}

// metalLBServiceStatusStatus mirrors MetalLBServiceL2Status and
// MetalLBServiceBGPStatus. Interfaces is set for L2 only.
type metalLBServiceStatusStatus struct {
	Node             string             `json:"node,omitempty"`
	ServiceName      string             `json:"serviceName,omitempty"`
	ServiceNamespace string             `json:"serviceNamespace,omitempty"`
	Interfaces       []metalLBInterface `json:"interfaces,omitempty"`
}

// metalLBInterface mirrors InterfaceInfo.
type metalLBInterface struct {
	Name string `json:"name,omitempty"`
}
