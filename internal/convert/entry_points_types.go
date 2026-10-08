// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

// Local structs mirroring the Gateway API objects. We convert the unstructured
// object into these plain structs with runtime.DefaultUnstructuredConverter and
// then convert to proto. Only the fields we send are mirrored, so the
// gateway-api module is not pulled in. These fields have the same names in
// v1alpha2, v1alpha3, v1beta1 and v1, and in every route kind, so one set of
// structs reads whichever version the cluster serves.
//
// Schema:
// - https://github.com/kubernetes-sigs/gateway-api/blob/main/apis/v1/gateway_types.go
// - https://github.com/kubernetes-sigs/gateway-api/blob/main/apis/v1/gatewayclass_types.go
// - https://github.com/kubernetes-sigs/gateway-api/blob/main/apis/v1/shared_types.go
// - https://github.com/kubernetes-sigs/gateway-api/blob/main/apis/v1/httproute_types.go

// gwGatewayClass mirrors the GatewayClass top-level object.
type gwGatewayClass struct {
	Spec gwGatewayClassSpec `json:"spec"`
}

// gwGatewayClassSpec mirrors GatewayClassSpec.
type gwGatewayClassSpec struct {
	ControllerName string `json:"controllerName"`
}

// gwGateway mirrors the Gateway top-level object.
type gwGateway struct {
	Spec   gwGatewaySpec   `json:"spec"`
	Status gwGatewayStatus `json:"status"`
}

// gwGatewaySpec mirrors GatewaySpec.
type gwGatewaySpec struct {
	GatewayClassName string       `json:"gatewayClassName"`
	Listeners        []gwListener `json:"listeners,omitempty"`
}

// gwListener mirrors Listener.
type gwListener struct {
	Name     string `json:"name"`
	Port     int32  `json:"port"`
	Protocol string `json:"protocol"`
}

// gwGatewayStatus mirrors GatewayStatus.
type gwGatewayStatus struct {
	Addresses []gwGatewayStatusAddress `json:"addresses,omitempty"`
}

// gwGatewayStatusAddress mirrors GatewayStatusAddress.
type gwGatewayStatusAddress struct {
	Type  *string `json:"type,omitempty"`
	Value string  `json:"value"`
}

// gwRoute mirrors the fields shared by HTTPRoute, GRPCRoute, TCPRoute, TLSRoute
// and UDPRoute.
type gwRoute struct {
	Spec   gwRouteSpec   `json:"spec"`
	Status gwRouteStatus `json:"status"`
}

// gwRouteSpec mirrors CommonRouteSpec plus the rules' backendRefs.
type gwRouteSpec struct {
	ParentRefs []gwParentReference `json:"parentRefs,omitempty"`
	Rules      []gwRouteRule       `json:"rules,omitempty"`
}

// gwParentReference mirrors ParentReference.
type gwParentReference struct {
	Group       *string `json:"group,omitempty"`
	Kind        *string `json:"kind,omitempty"`
	Namespace   *string `json:"namespace,omitempty"`
	Name        string  `json:"name"`
	SectionName *string `json:"sectionName,omitempty"`
	Port        *int32  `json:"port,omitempty"`
}

// gwRouteRule mirrors the backendRefs of HTTPRouteRule, GRPCRouteRule,
// TCPRouteRule, TLSRouteRule and UDPRouteRule.
type gwRouteRule struct {
	BackendRefs []gwBackendRef `json:"backendRefs,omitempty"`
}

// gwBackendRef mirrors BackendRef.
type gwBackendRef struct {
	Group     *string `json:"group,omitempty"`
	Kind      *string `json:"kind,omitempty"`
	Name      string  `json:"name"`
	Namespace *string `json:"namespace,omitempty"`
	Port      *int32  `json:"port,omitempty"`
	Weight    *int32  `json:"weight,omitempty"`
}

// gwRouteStatus mirrors RouteStatus.
type gwRouteStatus struct {
	Parents []gwRouteParentStatus `json:"parents,omitempty"`
}

// gwRouteParentStatus mirrors RouteParentStatus.
type gwRouteParentStatus struct {
	ParentRef      gwParentReference `json:"parentRef"`
	ControllerName string            `json:"controllerName"`
	Conditions     []gwCondition     `json:"conditions,omitempty"`
}

// gwCondition mirrors the metav1.Condition fields we read.
type gwCondition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}
