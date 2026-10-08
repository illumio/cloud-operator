// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

import (
	"fmt"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// GatewayAPIGroup is the API group of the Gateway API objects. Conversion
// matches on the group as well as the kind: config.openshift.io serves an
// Ingress kind and networking.istio.io a Gateway kind, with other schemas.
const GatewayAPIGroup = "gateway.networking.k8s.io"

// ingressClassAnnotation names an Ingress's class on Ingresses created before
// spec.ingressClassName existed. Many controllers still honor it.
const ingressClassAnnotation = "kubernetes.io/ingress.class"

// gatewayRouteKinds lists the Gateway API route kinds. They share the fields we
// send, so they share one converter.
var gatewayRouteKinds = map[string]bool{
	"GRPCRoute": true,
	"HTTPRoute": true,
	"TCPRoute":  true,
	"TLSRoute":  true,
	"UDPRoute":  true,
}

// convertEntryPoint sets KindSpecific for the objects that route traffic from
// outside the cluster to Services: Ingress, IngressClass and the Gateway API
// GatewayClass, Gateway and routes. Other objects are left unchanged.
func convertEntryPoint(objMetadata *pb.KubernetesObjectData, obj *unstructured.Unstructured, kind, apiGroup string) error {
	if obj == nil {
		return nil
	}

	switch {
	case kind == "Ingress" && apiGroup == networkingv1.GroupName:
		var ingress networkingv1.Ingress
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &ingress); err != nil {
			return fmt.Errorf("deserializing %s: %w", kind, err)
		}

		objMetadata.KindSpecific = &pb.KubernetesObjectData_Ingress{Ingress: convertIngress(&ingress)}
	case kind == "IngressClass" && apiGroup == networkingv1.GroupName:
		var ingressClass networkingv1.IngressClass
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &ingressClass); err != nil {
			return fmt.Errorf("deserializing %s: %w", kind, err)
		}

		objMetadata.KindSpecific = &pb.KubernetesObjectData_IngressClass{
			IngressClass: &pb.KubernetesIngressClassData{Controller: ingressClass.Spec.Controller},
		}
	case kind == "GatewayClass" && apiGroup == GatewayAPIGroup:
		var gatewayClass gwGatewayClass
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &gatewayClass); err != nil {
			return fmt.Errorf("deserializing %s: %w", kind, err)
		}

		objMetadata.KindSpecific = &pb.KubernetesObjectData_GatewayClass{
			GatewayClass: &pb.KubernetesGatewayClassData{ControllerName: gatewayClass.Spec.ControllerName},
		}
	case kind == "Gateway" && apiGroup == GatewayAPIGroup:
		var gateway gwGateway
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &gateway); err != nil {
			return fmt.Errorf("deserializing %s: %w", kind, err)
		}

		objMetadata.KindSpecific = &pb.KubernetesObjectData_Gateway{Gateway: convertGateway(&gateway)}
	case gatewayRouteKinds[kind] && apiGroup == GatewayAPIGroup:
		var route gwRoute
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &route); err != nil {
			return fmt.Errorf("deserializing %s: %w", kind, err)
		}

		objMetadata.KindSpecific = &pb.KubernetesObjectData_GatewayRoute{GatewayRoute: convertGatewayRoute(&route, obj.GetGeneration())}
	}

	return nil
}

func convertIngress(ingress *networkingv1.Ingress) *pb.KubernetesIngressData {
	spec := ingress.Spec

	out := &pb.KubernetesIngressData{
		IngressClassName:    nonEmptyString(spec.IngressClassName),
		LoadBalancerIngress: convertIngressLoadBalancerIngress(ingress.Status.LoadBalancer.Ingress),
	}

	if out.IngressClassName == nil {
		if class, ok := ingress.Annotations[ingressClassAnnotation]; ok {
			out.IngressClassName = nonEmptyString(&class)
		}
	}

	if spec.DefaultBackend != nil {
		out.DefaultBackend = convertIngressBackend(spec.DefaultBackend)
	}

	for _, rule := range spec.Rules {
		pbRule := &pb.KubernetesIngressData_Rule{Host: nonEmptyString(&rule.Host)}

		if rule.HTTP != nil {
			for _, path := range rule.HTTP.Paths {
				pbPath := &pb.KubernetesIngressData_Path{
					Path:    nonEmptyString(&path.Path),
					Backend: convertIngressBackend(&path.Backend),
				}

				if path.PathType != nil {
					pbPath.PathType = string(*path.PathType)
				}

				pbRule.Paths = append(pbRule.Paths, pbPath)
			}
		}

		out.Rules = append(out.Rules, pbRule)
	}

	for _, tls := range spec.TLS {
		out.Tls = append(out.Tls, &pb.KubernetesIngressData_TLS{
			Hosts:      tls.Hosts,
			SecretName: nonEmptyString(&tls.SecretName),
		})
	}

	return out
}

// convertIngressBackend converts a Service backend. A resource backend is
// returned with all fields unset.
func convertIngressBackend(backend *networkingv1.IngressBackend) *pb.KubernetesIngressData_Backend {
	out := &pb.KubernetesIngressData_Backend{}

	service := backend.Service
	if service == nil {
		return out
	}

	out.ServiceName = nonEmptyString(&service.Name)

	if service.Port.Number > 0 {
		out.ServicePortNumber = new(uint32(service.Port.Number))
	} else {
		out.ServicePortName = nonEmptyString(&service.Port.Name)
	}

	return out
}

// convertIngressLoadBalancerIngress converts an Ingress's
// status.loadBalancer.ingress into the LoadBalancerIngress message Services
// use. An Ingress has no ipMode.
func convertIngressLoadBalancerIngress(ingresses []networkingv1.IngressLoadBalancerIngress) []*pb.LoadBalancerIngress {
	if len(ingresses) == 0 {
		return nil
	}

	result := make([]*pb.LoadBalancerIngress, 0, len(ingresses))
	for _, ingress := range ingresses {
		entry := &pb.LoadBalancerIngress{
			Ip:       nonEmptyString(&ingress.IP),
			Hostname: nonEmptyString(&ingress.Hostname),
		}

		for _, portStatus := range ingress.Ports {
			entry.Ports = append(entry.Ports, &pb.LoadBalancerIngress_PortStatus{
				Port:     uint32(portStatus.Port), //nolint:gosec
				Protocol: string(portStatus.Protocol),
				Error:    portStatus.Error,
			})
		}

		result = append(result, entry)
	}

	return result
}

func convertGateway(gateway *gwGateway) *pb.KubernetesGatewayData {
	out := &pb.KubernetesGatewayData{GatewayClassName: gateway.Spec.GatewayClassName}

	for _, listener := range gateway.Spec.Listeners {
		out.Listeners = append(out.Listeners, &pb.KubernetesGatewayData_Listener{
			Name:     listener.Name,
			Port:     uint32(listener.Port), //nolint:gosec
			Protocol: listener.Protocol,
		})
	}

	for _, address := range gateway.Status.Addresses {
		out.Addresses = append(out.Addresses, &pb.KubernetesGatewayData_Address{
			Type:  nonEmptyString(address.Type),
			Value: address.Value,
		})
	}

	return out
}

// convertGatewayRoute converts a route. generation is the route's
// metadata.generation, used to drop an Accepted condition that describes an
// older spec.
func convertGatewayRoute(route *gwRoute, generation int64) *pb.KubernetesGatewayRouteData {
	out := &pb.KubernetesGatewayRouteData{}

	for _, parentRef := range route.Spec.ParentRefs {
		out.ParentRefs = append(out.ParentRefs, convertGatewayParentReference(&parentRef))
	}

	for _, rule := range route.Spec.Rules {
		for _, backendRef := range rule.BackendRefs {
			out.BackendRefs = append(out.BackendRefs, &pb.KubernetesGatewayRouteData_BackendRef{
				Group:     backendRef.Group,
				Kind:      backendRef.Kind,
				Name:      backendRef.Name,
				Namespace: nonEmptyString(backendRef.Namespace),
				Port:      int32PtrToUint32(backendRef.Port),
				Weight:    backendRef.Weight,
			})
		}
	}

	for _, parent := range route.Status.Parents {
		pbParent := &pb.KubernetesGatewayRouteData_RouteParentStatus{
			ParentRef:      convertGatewayParentReference(&parent.ParentRef),
			ControllerName: parent.ControllerName,
		}

		for _, condition := range parent.Conditions {
			if condition.Type != "Accepted" {
				continue
			}

			// The condition describes an older spec: whether the current one is
			// accepted is not known yet, so accepted stays unset. Without
			// observedGeneration staleness cannot be told, and the condition is
			// taken as current.
			if condition.ObservedGeneration > 0 && condition.ObservedGeneration < generation {
				break
			}

			switch metav1.ConditionStatus(condition.Status) {
			case metav1.ConditionTrue:
				pbParent.Accepted = new(true)
			case metav1.ConditionFalse:
				pbParent.Accepted = new(false)
			case metav1.ConditionUnknown:
				// Not decided yet: accepted stays unset.
			}

			pbParent.AcceptedReason = nonEmptyString(&condition.Reason)

			break
		}

		out.Parents = append(out.Parents, pbParent)
	}

	return out
}

// convertGatewayParentReference converts a ParentReference. Group and kind are
// kept even when empty: an empty group names the core group, as in a GAMMA
// parentRef to a Service.
func convertGatewayParentReference(parentRef *gwParentReference) *pb.KubernetesGatewayRouteData_ParentReference {
	return &pb.KubernetesGatewayRouteData_ParentReference{
		Group:       parentRef.Group,
		Kind:        parentRef.Kind,
		Namespace:   nonEmptyString(parentRef.Namespace),
		Name:        parentRef.Name,
		SectionName: nonEmptyString(parentRef.SectionName),
		Port:        int32PtrToUint32(parentRef.Port),
	}
}

// int32PtrToUint32 converts an optional port to uint32. Unlike int32ToUint32 it
// returns nil, rather than panicking, for a negative value from a malformed object.
func int32PtrToUint32(i *int32) *uint32 {
	if i == nil || *i < 0 {
		return nil
	}

	return new(uint32(*i))
}
