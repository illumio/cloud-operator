// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// IsAWSNetworkPolicy returns true if the resource kind is an AWS VPC CNI network policy.
func IsAWSNetworkPolicy(kind string) bool {
	return kind == "ClusterNetworkPolicy" || kind == "SecurityGroupPolicy"
}

// ConvertUnstructuredToAWSNetworkPolicy converts an unstructured AWS network policy to a KubernetesObjectData proto.
// This handles both ClusterNetworkPolicy and SecurityGroupPolicy.
func ConvertUnstructuredToAWSNetworkPolicy(obj *unstructured.Unstructured) (*pb.KubernetesObjectData, error) {
	if obj == nil {
		return nil, nil
	}

	kind := obj.GetKind()
	objMetadata := &pb.KubernetesObjectData{
		Annotations:       obj.GetAnnotations(),
		CreationTimestamp: timestamppb.New(obj.GetCreationTimestamp().Time),
		Kind:              kind,
		Labels:            obj.GetLabels(),
		Name:              obj.GetName(),
		Namespace:         obj.GetNamespace(),
		OwnerReferences:   convertOwnerReferences(obj.GetOwnerReferences()),
		ResourceVersion:   obj.GetResourceVersion(),
		Uid:               string(obj.GetUID()),
	}

	spec, found, err := unstructured.NestedMap(obj.Object, "spec")
	if err != nil {
		return nil, fmt.Errorf("failed to extract spec from %s %q: %w", kind, obj.GetName(), err)
	}
	if !found {
		return objMetadata, nil
	}

	switch kind {
	case "ClusterNetworkPolicy":
		policy, err := convertAWSClusterNetworkPolicySpec(spec)
		if err != nil {
			return nil, fmt.Errorf("failed to convert ClusterNetworkPolicy %q spec: %w", obj.GetName(), err)
		}
		objMetadata.KindSpecific = &pb.KubernetesObjectData_AwsClusterNetworkPolicy{AwsClusterNetworkPolicy: policy}
	case "SecurityGroupPolicy":
		policy, err := convertAWSSecurityGroupPolicySpec(spec)
		if err != nil {
			return nil, fmt.Errorf("failed to convert SecurityGroupPolicy %q spec: %w", obj.GetName(), err)
		}
		objMetadata.KindSpecific = &pb.KubernetesObjectData_AwsSecurityGroupPolicy{AwsSecurityGroupPolicy: policy}
	}

	return objMetadata, nil
}

// convertAWSClusterNetworkPolicySpec converts an AWS ClusterNetworkPolicy spec to proto.
func convertAWSClusterNetworkPolicySpec(spec map[string]any) (*pb.KubernetesAWSClusterNetworkPolicyData, error) {
	policy := &pb.KubernetesAWSClusterNetworkPolicyData{}

	if priority, found, err := unstructured.NestedInt64(spec, "priority"); err != nil {
		return nil, fmt.Errorf("failed to extract priority: %w", err)
	} else if found {
		policy.Priority = int32(priority)
	}

	if tier, found, err := unstructured.NestedString(spec, "tier"); err != nil {
		return nil, fmt.Errorf("failed to extract tier: %w", err)
	} else if found {
		policy.Tier = tier
	}

	if subject, found, err := unstructured.NestedMap(spec, "subject"); err != nil {
		return nil, fmt.Errorf("failed to extract subject: %w", err)
	} else if found {
		subjectProto, err := convertAWSNetworkPolicySubject(subject)
		if err != nil {
			return nil, fmt.Errorf("failed to convert subject: %w", err)
		}
		policy.Subject = subjectProto
	}

	if ingress, found, err := unstructured.NestedSlice(spec, "ingress"); err != nil {
		return nil, fmt.Errorf("failed to extract ingress rules: %w", err)
	} else if found {
		rules, err := convertAWSNetworkPolicyRules(ingress)
		if err != nil {
			return nil, fmt.Errorf("failed to convert ingress rules: %w", err)
		}
		policy.IngressRules = rules
	}

	if egress, found, err := unstructured.NestedSlice(spec, "egress"); err != nil {
		return nil, fmt.Errorf("failed to extract egress rules: %w", err)
	} else if found {
		rules, err := convertAWSNetworkPolicyRules(egress)
		if err != nil {
			return nil, fmt.Errorf("failed to convert egress rules: %w", err)
		}
		policy.EgressRules = rules
	}

	return policy, nil
}

// convertAWSSecurityGroupPolicySpec converts an AWS SecurityGroupPolicy spec to proto.
func convertAWSSecurityGroupPolicySpec(spec map[string]any) (*pb.KubernetesAWSSecurityGroupPolicyData, error) {
	policy := &pb.KubernetesAWSSecurityGroupPolicyData{}

	if podSelector, found, err := unstructured.NestedMap(spec, "podSelector"); err != nil {
		return nil, fmt.Errorf("failed to extract podSelector: %w", err)
	} else if found {
		selector, err := convertAWSLabelSelector(podSelector)
		if err != nil {
			return nil, fmt.Errorf("failed to convert podSelector: %w", err)
		}
		policy.PodSelector = selector
	}

	if saSelector, found, err := unstructured.NestedMap(spec, "serviceAccountSelector"); err != nil {
		return nil, fmt.Errorf("failed to extract serviceAccountSelector: %w", err)
	} else if found {
		selector, err := convertAWSLabelSelector(saSelector)
		if err != nil {
			return nil, fmt.Errorf("failed to convert serviceAccountSelector: %w", err)
		}
		policy.ServiceAccountSelector = selector
	}

	if groupIds, found, err := unstructured.NestedStringSlice(spec, "securityGroups", "groupIds"); err != nil {
		return nil, fmt.Errorf("failed to extract securityGroups.groupIds: %w", err)
	} else if found {
		policy.SecurityGroupIds = groupIds
	}

	return policy, nil
}

// convertAWSNetworkPolicySubject converts the subject field of a ClusterNetworkPolicy.
// Subject can be either pods (with namespace and pod selectors) or namespaces.
func convertAWSNetworkPolicySubject(subject map[string]any) (*pb.AWSNetworkPolicySubject, error) {
	if subject == nil {
		return nil, nil
	}

	result := &pb.AWSNetworkPolicySubject{}

	if pods, found, err := unstructured.NestedMap(subject, "pods"); err != nil {
		return nil, fmt.Errorf("failed to extract pods: %w", err)
	} else if found {
		podSelector, err := convertAWSNetworkPolicyPodSelector(pods)
		if err != nil {
			return nil, fmt.Errorf("failed to convert pods selector: %w", err)
		}
		result.Pods = podSelector
	}

	if namespaces, found, err := unstructured.NestedMap(subject, "namespaces"); err != nil {
		return nil, fmt.Errorf("failed to extract namespaces: %w", err)
	} else if found {
		nsSelector, err := convertAWSLabelSelector(namespaces)
		if err != nil {
			return nil, fmt.Errorf("failed to convert namespaces selector: %w", err)
		}
		result.Namespaces = nsSelector
	}

	return result, nil
}

// convertAWSNetworkPolicyPodSelector converts a pod selector with namespace and pod selectors.
func convertAWSNetworkPolicyPodSelector(pods map[string]any) (*pb.AWSNetworkPolicyPodSelector, error) {
	if pods == nil {
		return nil, nil
	}

	result := &pb.AWSNetworkPolicyPodSelector{}

	if nsSelector, found, err := unstructured.NestedMap(pods, "namespaceSelector"); err != nil {
		return nil, fmt.Errorf("failed to extract namespaceSelector: %w", err)
	} else if found {
		selector, err := convertAWSLabelSelector(nsSelector)
		if err != nil {
			return nil, fmt.Errorf("failed to convert namespaceSelector: %w", err)
		}
		result.NamespaceSelector = selector
	}

	if podSelector, found, err := unstructured.NestedMap(pods, "podSelector"); err != nil {
		return nil, fmt.Errorf("failed to extract podSelector: %w", err)
	} else if found {
		selector, err := convertAWSLabelSelector(podSelector)
		if err != nil {
			return nil, fmt.Errorf("failed to convert podSelector: %w", err)
		}
		result.PodSelector = selector
	}

	return result, nil
}

// convertAWSLabelSelector converts a label selector from unstructured to proto.
func convertAWSLabelSelector(selector map[string]any) (*pb.LabelSelector, error) {
	if selector == nil {
		return nil, nil
	}

	result := &pb.LabelSelector{}

	if matchLabels, found, err := unstructured.NestedStringMap(selector, "matchLabels"); err != nil {
		return nil, fmt.Errorf("failed to extract matchLabels: %w", err)
	} else if found {
		result.MatchLabels = matchLabels
	}

	if matchExpressions, found, err := unstructured.NestedSlice(selector, "matchExpressions"); err != nil {
		return nil, fmt.Errorf("failed to extract matchExpressions: %w", err)
	} else if found {
		expressions, err := convertAWSMatchExpressions(matchExpressions)
		if err != nil {
			return nil, fmt.Errorf("failed to convert matchExpressions: %w", err)
		}
		result.MatchExpressions = expressions
	}

	return result, nil
}

// convertAWSMatchExpressions converts match expressions from unstructured to proto.
func convertAWSMatchExpressions(expressions []any) ([]*pb.LabelSelectorRequirement, error) {
	if len(expressions) == 0 {
		return nil, nil
	}

	result := make([]*pb.LabelSelectorRequirement, 0, len(expressions))
	for i, expr := range expressions {
		exprMap, ok := expr.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("matchExpression[%d] is not a map", i)
		}

		req := &pb.LabelSelectorRequirement{}

		if key, found, err := unstructured.NestedString(exprMap, "key"); err != nil {
			return nil, fmt.Errorf("failed to extract key from matchExpression[%d]: %w", i, err)
		} else if found {
			req.Key = key
		}

		if operator, found, err := unstructured.NestedString(exprMap, "operator"); err != nil {
			return nil, fmt.Errorf("failed to extract operator from matchExpression[%d]: %w", i, err)
		} else if found {
			req.Operator = operator
		}

		if values, found, err := unstructured.NestedStringSlice(exprMap, "values"); err != nil {
			return nil, fmt.Errorf("failed to extract values from matchExpression[%d]: %w", i, err)
		} else if found {
			req.Values = values
		}

		result = append(result, req)
	}

	return result, nil
}

// convertAWSNetworkPolicyRules converts AWS network policy rules from unstructured to proto.
func convertAWSNetworkPolicyRules(rules []any) ([]*pb.AWSNetworkPolicyRule, error) {
	if len(rules) == 0 {
		return nil, nil
	}

	result := make([]*pb.AWSNetworkPolicyRule, 0, len(rules))
	for i, rule := range rules {
		ruleMap, ok := rule.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("rule[%d] is not a map", i)
		}

		protoRule := &pb.AWSNetworkPolicyRule{}

		if action, found, err := unstructured.NestedString(ruleMap, "action"); err != nil {
			return nil, fmt.Errorf("failed to extract action from rule[%d]: %w", i, err)
		} else if found {
			protoRule.Action = action
		}

		if ports, found, err := unstructured.NestedSlice(ruleMap, "ports"); err != nil {
			return nil, fmt.Errorf("failed to extract ports from rule[%d]: %w", i, err)
		} else if found {
			convertedPorts, err := convertAWSNetworkPolicyPorts(ports)
			if err != nil {
				return nil, fmt.Errorf("failed to convert ports in rule[%d]: %w", i, err)
			}
			protoRule.Ports = convertedPorts
		}

		if from, found, err := unstructured.NestedSlice(ruleMap, "from"); err != nil {
			return nil, fmt.Errorf("failed to extract from peers in rule[%d]: %w", i, err)
		} else if found {
			peers, err := convertAWSNetworkPolicyPeers(from)
			if err != nil {
				return nil, fmt.Errorf("failed to convert from peers in rule[%d]: %w", i, err)
			}
			protoRule.From = peers
		}

		if to, found, err := unstructured.NestedSlice(ruleMap, "to"); err != nil {
			return nil, fmt.Errorf("failed to extract to peers in rule[%d]: %w", i, err)
		} else if found {
			peers, err := convertAWSNetworkPolicyPeers(to)
			if err != nil {
				return nil, fmt.Errorf("failed to convert to peers in rule[%d]: %w", i, err)
			}
			protoRule.To = peers
		}

		result = append(result, protoRule)
	}

	return result, nil
}

// convertAWSNetworkPolicyPorts converts port specifications from unstructured to proto.
func convertAWSNetworkPolicyPorts(ports []any) ([]*pb.AWSNetworkPolicyPort, error) {
	if len(ports) == 0 {
		return nil, nil
	}

	result := make([]*pb.AWSNetworkPolicyPort, 0, len(ports))
	for i, port := range ports {
		portMap, ok := port.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("port[%d] is not a map", i)
		}

		protoPort := &pb.AWSNetworkPolicyPort{}

		if portNumber, found, err := unstructured.NestedMap(portMap, "portNumber"); err != nil {
			return nil, fmt.Errorf("failed to extract portNumber from port[%d]: %w", i, err)
		} else if found {
			pn, err := convertAWSPortNumber(portNumber)
			if err != nil {
				return nil, fmt.Errorf("failed to convert portNumber in port[%d]: %w", i, err)
			}
			protoPort.PortNumber = pn
		}

		result = append(result, protoPort)
	}

	return result, nil
}

// convertAWSPortNumber converts a port number specification from unstructured to proto.
func convertAWSPortNumber(portNumber map[string]any) (*pb.AWSPortNumber, error) {
	if portNumber == nil {
		return nil, nil
	}

	result := &pb.AWSPortNumber{}

	if port, found, err := unstructured.NestedInt64(portNumber, "port"); err != nil {
		return nil, fmt.Errorf("failed to extract port: %w", err)
	} else if found {
		result.Port = int32(port)
	}

	if protocol, found, err := unstructured.NestedString(portNumber, "protocol"); err != nil {
		return nil, fmt.Errorf("failed to extract protocol: %w", err)
	} else if found {
		result.Protocol = protocol
	}

	return result, nil
}

// convertAWSNetworkPolicyPeers converts peer specifications from unstructured to proto.
func convertAWSNetworkPolicyPeers(peers []any) ([]*pb.AWSNetworkPolicyPeer, error) {
	if len(peers) == 0 {
		return nil, nil
	}

	result := make([]*pb.AWSNetworkPolicyPeer, 0, len(peers))
	for i, peer := range peers {
		peerMap, ok := peer.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("peer[%d] is not a map", i)
		}

		protoPeer := &pb.AWSNetworkPolicyPeer{}

		if networks, found, err := unstructured.NestedStringSlice(peerMap, "networks"); err != nil {
			return nil, fmt.Errorf("failed to extract networks from peer[%d]: %w", i, err)
		} else if found {
			protoPeer.Networks = networks
		}

		if pods, found, err := unstructured.NestedMap(peerMap, "pods"); err != nil {
			return nil, fmt.Errorf("failed to extract pods from peer[%d]: %w", i, err)
		} else if found {
			podSelector, err := convertAWSNetworkPolicyPodSelector(pods)
			if err != nil {
				return nil, fmt.Errorf("failed to convert pods selector in peer[%d]: %w", i, err)
			}
			protoPeer.Pods = podSelector
		}

		if namespaces, found, err := unstructured.NestedMap(peerMap, "namespaces"); err != nil {
			return nil, fmt.Errorf("failed to extract namespaces from peer[%d]: %w", i, err)
		} else if found {
			nsSelector, err := convertAWSLabelSelector(namespaces)
			if err != nil {
				return nil, fmt.Errorf("failed to convert namespaces selector in peer[%d]: %w", i, err)
			}
			protoPeer.Namespaces = nsSelector
		}

		if domainNames, found, err := unstructured.NestedStringSlice(peerMap, "domainNames"); err != nil {
			return nil, fmt.Errorf("failed to extract domainNames from peer[%d]: %w", i, err)
		} else if found {
			protoPeer.DomainNames = domainNames
		}

		result = append(result, protoPeer)
	}

	return result, nil
}
