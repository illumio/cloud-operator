// Copyright 2026 Illumio, Inc. All Rights Reserved.

package convert

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

func TestConvertIngress(t *testing.T) {
	tests := map[string]struct {
		annotations map[string]any
		spec        map[string]any
		status      map[string]any
		expected    *pb.KubernetesIngressData
	}{
		"rules, default backend, tls and load balancer status": {
			spec: map[string]any{
				"ingressClassName": "nginx",
				"defaultBackend": map[string]any{
					"service": map[string]any{"name": "fallback", "port": map[string]any{"name": "http"}},
				},
				"rules": []any{
					map[string]any{
						"host": "shop.example.com",
						"http": map[string]any{"paths": []any{
							map[string]any{
								"path":     "/api",
								"pathType": "Prefix",
								"backend": map[string]any{
									"service": map[string]any{"name": "api", "port": map[string]any{"number": int64(8080)}},
								},
							},
							map[string]any{
								"pathType": "ImplementationSpecific",
								"backend": map[string]any{
									"service": map[string]any{"name": "web", "port": map[string]any{"name": "https"}},
								},
							},
						}},
					},
					// A rule without a host matches every host.
					map[string]any{
						"http": map[string]any{"paths": []any{
							map[string]any{
								"path":     "/",
								"pathType": "Exact",
								"backend": map[string]any{
									"service": map[string]any{"name": "web", "port": map[string]any{"number": int64(80)}},
								},
							},
						}},
					},
				},
				"tls": []any{
					map[string]any{"hosts": []any{"shop.example.com"}, "secretName": "shop-tls"},
				},
			},
			status: map[string]any{
				"loadBalancer": map[string]any{"ingress": []any{
					map[string]any{"ip": "203.0.113.10"},
					map[string]any{
						"hostname": "lb.example.com",
						"ports": []any{
							map[string]any{"port": int64(443), "protocol": "TCP", "error": "PortNotAllocated"},
						},
					},
				}},
			},
			expected: &pb.KubernetesIngressData{
				IngressClassName: new("nginx"),
				DefaultBackend: &pb.KubernetesIngressData_Backend{
					ServiceName:     new("fallback"),
					ServicePortName: new("http"),
				},
				Rules: []*pb.KubernetesIngressData_Rule{
					{
						Host: new("shop.example.com"),
						Paths: []*pb.KubernetesIngressData_Path{
							{
								Path:     new("/api"),
								PathType: "Prefix",
								Backend: &pb.KubernetesIngressData_Backend{
									ServiceName:       new("api"),
									ServicePortNumber: new(uint32(8080)),
								},
							},
							{
								PathType: "ImplementationSpecific",
								Backend: &pb.KubernetesIngressData_Backend{
									ServiceName:     new("web"),
									ServicePortName: new("https"),
								},
							},
						},
					},
					{
						Paths: []*pb.KubernetesIngressData_Path{{
							Path:     new("/"),
							PathType: "Exact",
							Backend: &pb.KubernetesIngressData_Backend{
								ServiceName:       new("web"),
								ServicePortNumber: new(uint32(80)),
							},
						}},
					},
				},
				Tls: []*pb.KubernetesIngressData_TLS{
					{Hosts: []string{"shop.example.com"}, SecretName: new("shop-tls")},
				},
				LoadBalancerIngress: []*pb.LoadBalancerIngress{
					{Ip: new("203.0.113.10")},
					{
						Hostname: new("lb.example.com"),
						Ports: []*pb.LoadBalancerIngress_PortStatus{
							{Port: 443, Protocol: "TCP", Error: new("PortNotAllocated")},
						},
					},
				},
			},
		},
		"legacy class annotation is used when spec.ingressClassName is unset": {
			annotations: map[string]any{ingressClassAnnotation: "alb"},
			spec: map[string]any{
				"defaultBackend": map[string]any{
					"service": map[string]any{"name": "web", "port": map[string]any{"number": int64(80)}},
				},
			},
			expected: &pb.KubernetesIngressData{
				IngressClassName: new("alb"),
				DefaultBackend: &pb.KubernetesIngressData_Backend{
					ServiceName:       new("web"),
					ServicePortNumber: new(uint32(80)),
				},
			},
		},
		"spec.ingressClassName wins over the legacy annotation": {
			annotations: map[string]any{ingressClassAnnotation: "alb"},
			spec:        map[string]any{"ingressClassName": "nginx"},
			expected:    &pb.KubernetesIngressData{IngressClassName: new("nginx")},
		},
		"empty legacy annotation leaves the class unset": {
			annotations: map[string]any{ingressClassAnnotation: ""},
			spec:        map[string]any{},
			expected:    &pb.KubernetesIngressData{},
		},
		"rule with a host and no http block": {
			spec: map[string]any{
				"rules": []any{map[string]any{"host": "shop.example.com"}},
			},
			expected: &pb.KubernetesIngressData{
				Rules: []*pb.KubernetesIngressData_Rule{{Host: new("shop.example.com")}},
			},
		},
		// Objects stored before pathType was required may have none.
		"path without pathType": {
			spec: map[string]any{
				"rules": []any{map[string]any{
					"http": map[string]any{"paths": []any{map[string]any{
						"path": "/",
						"backend": map[string]any{
							"service": map[string]any{"name": "web", "port": map[string]any{"number": int64(80)}},
						},
					}}},
				}},
			},
			expected: &pb.KubernetesIngressData{
				Rules: []*pb.KubernetesIngressData_Rule{{
					Paths: []*pb.KubernetesIngressData_Path{{
						Path: new("/"),
						Backend: &pb.KubernetesIngressData_Backend{
							ServiceName:       new("web"),
							ServicePortNumber: new(uint32(80)),
						},
					}},
				}},
			},
		},
		// An empty Ingress still gets KindSpecific, so the receiver can tell it
		// was read.
		"no spec or status": {
			expected: &pb.KubernetesIngressData{},
		},
		"no class and a resource backend": {
			spec: map[string]any{
				"defaultBackend": map[string]any{
					"resource": map[string]any{"apiGroup": "k8s.example.com", "kind": "StorageBucket", "name": "static"},
				},
			},
			expected: &pb.KubernetesIngressData{
				DefaultBackend: &pb.KubernetesIngressData_Backend{},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			metadata := map[string]any{"name": "shop", "namespace": "default"}
			if tt.annotations != nil {
				metadata["annotations"] = tt.annotations
			}

			obj := map[string]any{
				"apiVersion": "networking.k8s.io/v1",
				"kind":       "Ingress",
				"metadata":   metadata,
			}
			if tt.spec != nil {
				obj["spec"] = tt.spec
			}

			if tt.status != nil {
				obj["status"] = tt.status
			}

			result := convertBinding(t, obj)

			assert.Equal(t, tt.expected, result.GetIngress())
		})
	}
}

func TestConvertIngressClass(t *testing.T) {
	result := convertBinding(t, map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "IngressClass",
		"metadata":   map[string]any{"name": "nginx"},
		"spec":       map[string]any{"controller": "k8s.io/ingress-nginx"},
	})

	assert.Equal(t, &pb.KubernetesIngressClassData{Controller: "k8s.io/ingress-nginx"}, result.GetIngressClass())
}

func TestConvertGatewayClass(t *testing.T) {
	result := convertBinding(t, map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "GatewayClass",
		"metadata":   map[string]any{"name": "eg"},
		"spec":       map[string]any{"controllerName": "gateway.envoyproxy.io/gatewayclass-controller"},
	})

	assert.Equal(t, &pb.KubernetesGatewayClassData{
		ControllerName: "gateway.envoyproxy.io/gatewayclass-controller",
	}, result.GetGatewayClass())
}

func TestConvertGateway(t *testing.T) {
	// v1beta1 and v1alpha2 are served by Gateway API installs older than v1.0
	// and v0.6. The fields read are the same as in v1.
	for _, apiVersion := range []string{
		"gateway.networking.k8s.io/v1",
		"gateway.networking.k8s.io/v1beta1",
		"gateway.networking.k8s.io/v1alpha2",
	} {
		t.Run(apiVersion, func(t *testing.T) {
			result := convertBinding(t, map[string]any{
				"apiVersion": apiVersion,
				"kind":       "Gateway",
				"metadata":   map[string]any{"name": "edge", "namespace": "infra"},
				"spec": map[string]any{
					"gatewayClassName": "eg",
					"listeners": []any{
						map[string]any{"name": "http", "port": int64(80), "protocol": "HTTP"},
						map[string]any{"name": "tls", "port": int64(443), "protocol": "TLS", "hostname": "*.example.com"},
					},
				},
				"status": map[string]any{
					"addresses": []any{
						map[string]any{"type": "IPAddress", "value": "203.0.113.20"},
						map[string]any{"type": "Hostname", "value": "edge.example.com"},
						map[string]any{"value": "203.0.113.21"},
					},
				},
			})

			assert.Equal(t, &pb.KubernetesGatewayData{
				GatewayClassName: "eg",
				Listeners: []*pb.KubernetesGatewayData_Listener{
					{Name: "http", Port: 80, Protocol: "HTTP"},
					{Name: "tls", Port: 443, Protocol: "TLS"},
				},
				Addresses: []*pb.KubernetesGatewayData_Address{
					{Type: new("IPAddress"), Value: "203.0.113.20"},
					{Type: new("Hostname"), Value: "edge.example.com"},
					{Value: "203.0.113.21"},
				},
			}, result.GetGateway())
		})
	}
}

// A Gateway that is not programmed yet has no status.
func TestConvertGateway_NoStatus(t *testing.T) {
	result := convertBinding(t, map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "Gateway",
		"metadata":   map[string]any{"name": "edge", "namespace": "infra"},
		"spec": map[string]any{
			"gatewayClassName": "eg",
			"listeners":        []any{map[string]any{"name": "http", "port": int64(80), "protocol": "HTTP"}},
		},
	})

	assert.Equal(t, &pb.KubernetesGatewayData{
		GatewayClassName: "eg",
		Listeners:        []*pb.KubernetesGatewayData_Listener{{Name: "http", Port: 80, Protocol: "HTTP"}},
	}, result.GetGateway())
}

// A route that no controller has processed yet has no status.
func TestConvertGatewayRoute_NoStatus(t *testing.T) {
	result := convertBinding(t, map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata":   map[string]any{"name": "shop", "namespace": "default"},
		"spec": map[string]any{
			"parentRefs": []any{map[string]any{"name": "edge"}},
		},
	})

	assert.Equal(t, &pb.KubernetesGatewayRouteData{
		ParentRefs: []*pb.KubernetesGatewayRouteData_ParentReference{{Name: "edge"}},
	}, result.GetGatewayRoute())
}

func TestConvertHTTPRoute(t *testing.T) {
	gatewayRef := map[string]any{
		"group":       "gateway.networking.k8s.io",
		"kind":        "Gateway",
		"namespace":   "infra",
		"name":        "edge",
		"sectionName": "https",
	}

	result := convertBinding(t, map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata":   map[string]any{"name": "shop", "namespace": "default"},
		"spec": map[string]any{
			"parentRefs": []any{
				gatewayRef,
				// A GAMMA (service mesh) parent: the route attaches to a Service.
				map[string]any{"group": "", "kind": "Service", "name": "web", "port": int64(8080)},
			},
			"rules": []any{
				map[string]any{
					"matches": []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/api"}}},
					"backendRefs": []any{
						map[string]any{"name": "api", "port": int64(8080), "weight": int64(90)},
						map[string]any{"name": "api-canary", "namespace": "canary", "port": int64(8080), "weight": int64(10)},
					},
				},
				map[string]any{
					"backendRefs": []any{
						map[string]any{"group": "", "kind": "Service", "name": "web", "port": int64(80)},
					},
				},
				// A rule without backends (e.g. a redirect).
				map[string]any{
					"filters": []any{map[string]any{"type": "RequestRedirect"}},
				},
			},
		},
		"status": map[string]any{
			"parents": []any{
				map[string]any{
					"parentRef":      gatewayRef,
					"controllerName": "gateway.envoyproxy.io/gatewayclass-controller",
					"conditions": []any{
						map[string]any{"type": "ResolvedRefs", "status": "True", "reason": "ResolvedRefs"},
						map[string]any{
							"type":               "Accepted",
							"status":             "True",
							"reason":             "Accepted",
							"lastTransitionTime": "2026-10-07T10:00:00Z",
						},
					},
				},
				map[string]any{
					"parentRef":      map[string]any{"name": "internal"},
					"controllerName": "example.com/controller",
					"conditions": []any{
						map[string]any{"type": "Accepted", "status": "False", "reason": "NotAllowedByListeners"},
					},
				},
				map[string]any{
					"parentRef":      map[string]any{"name": "pending"},
					"controllerName": "example.com/controller",
					"conditions": []any{
						map[string]any{"type": "Accepted", "status": "Unknown", "reason": "Pending"},
					},
				},
				map[string]any{
					"parentRef":      map[string]any{"name": "no-conditions"},
					"controllerName": "example.com/controller",
				},
			},
		},
	})

	gatewayParent := &pb.KubernetesGatewayRouteData_ParentReference{
		Group:       new("gateway.networking.k8s.io"),
		Kind:        new("Gateway"),
		Namespace:   new("infra"),
		Name:        "edge",
		SectionName: new("https"),
	}

	assert.Equal(t, &pb.KubernetesGatewayRouteData{
		ParentRefs: []*pb.KubernetesGatewayRouteData_ParentReference{
			gatewayParent,
			{Group: new(""), Kind: new("Service"), Name: "web", Port: new(uint32(8080))},
		},
		BackendRefs: []*pb.KubernetesGatewayRouteData_BackendRef{
			{Name: "api", Port: new(uint32(8080)), Weight: new(int32(90))},
			{Name: "api-canary", Namespace: new("canary"), Port: new(uint32(8080)), Weight: new(int32(10))},
			{Group: new(""), Kind: new("Service"), Name: "web", Port: new(uint32(80))},
		},
		Parents: []*pb.KubernetesGatewayRouteData_RouteParentStatus{
			{
				ParentRef:      gatewayParent,
				ControllerName: "gateway.envoyproxy.io/gatewayclass-controller",
				Accepted:       new(true),
				AcceptedReason: new("Accepted"),
			},
			{
				ParentRef:      &pb.KubernetesGatewayRouteData_ParentReference{Name: "internal"},
				ControllerName: "example.com/controller",
				Accepted:       new(false),
				AcceptedReason: new("NotAllowedByListeners"),
			},
			{
				ParentRef:      &pb.KubernetesGatewayRouteData_ParentReference{Name: "pending"},
				ControllerName: "example.com/controller",
				AcceptedReason: new("Pending"),
			},
			{
				ParentRef:      &pb.KubernetesGatewayRouteData_ParentReference{Name: "no-conditions"},
				ControllerName: "example.com/controller",
			},
		},
	}, result.GetGatewayRoute())
}

// After a route's spec changes, its Accepted condition can still describe the
// previous generation, e.g. while a controller keeps serving the last valid
// configuration. Acceptance of an older generation is not sent with the new
// backends.
func TestConvertGatewayRoute_StaleAccepted(t *testing.T) {
	tests := map[string]struct {
		generation         int64
		observedGeneration int64
		expectedAccepted   *bool
		expectedReason     *string
	}{
		"condition observed the current generation": {
			generation:         3,
			observedGeneration: 3,
			expectedAccepted:   new(true),
			expectedReason:     new("Accepted"),
		},
		"condition observed an older generation": {
			generation:         3,
			observedGeneration: 2,
		},
		// Controllers are not required to set observedGeneration; without it
		// staleness cannot be told, and the condition is taken as current.
		"condition without observedGeneration": {
			generation:       3,
			expectedAccepted: new(true),
			expectedReason:   new("Accepted"),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			condition := map[string]any{"type": "Accepted", "status": "True", "reason": "Accepted"}
			if tt.observedGeneration != 0 {
				condition["observedGeneration"] = tt.observedGeneration
			}

			result := convertBinding(t, map[string]any{
				"apiVersion": "gateway.networking.k8s.io/v1",
				"kind":       "HTTPRoute",
				"metadata":   map[string]any{"name": "shop", "namespace": "default", "generation": tt.generation},
				"spec": map[string]any{
					"parentRefs": []any{map[string]any{"name": "edge"}},
					"rules": []any{map[string]any{
						"backendRefs": []any{map[string]any{"name": "web-v2", "port": int64(80)}},
					}},
				},
				"status": map[string]any{
					"parents": []any{map[string]any{
						"parentRef":      map[string]any{"name": "edge"},
						"controllerName": "example.com/controller",
						"conditions":     []any{condition},
					}},
				},
			})

			parents := result.GetGatewayRoute().GetParents()
			require.Len(t, parents, 1)
			assert.Equal(t, "example.com/controller", parents[0].GetControllerName())
			assert.Equal(t, tt.expectedAccepted, parents[0].Accepted)
			assert.Equal(t, tt.expectedReason, parents[0].AcceptedReason)
			assert.Equal(t, []*pb.KubernetesGatewayRouteData_BackendRef{{Name: "web-v2", Port: new(uint32(80))}},
				result.GetGatewayRoute().GetBackendRefs())
		})
	}
}

// Every route kind is converted the same way, at whichever version the cluster
// serves it: GRPCRoute was v1alpha2 before Gateway API v1.1, and TCPRoute,
// TLSRoute and UDPRoute are alpha only.
func TestConvertGatewayRoute_Kinds(t *testing.T) {
	tests := []struct {
		kind       string
		apiVersion string
	}{
		{kind: "GRPCRoute", apiVersion: "gateway.networking.k8s.io/v1"},
		{kind: "GRPCRoute", apiVersion: "gateway.networking.k8s.io/v1alpha2"},
		{kind: "HTTPRoute", apiVersion: "gateway.networking.k8s.io/v1"},
		{kind: "HTTPRoute", apiVersion: "gateway.networking.k8s.io/v1beta1"},
		{kind: "HTTPRoute", apiVersion: "gateway.networking.k8s.io/v1alpha2"},
		{kind: "TCPRoute", apiVersion: "gateway.networking.k8s.io/v1alpha2"},
		{kind: "TLSRoute", apiVersion: "gateway.networking.k8s.io/v1alpha2"},
		{kind: "TLSRoute", apiVersion: "gateway.networking.k8s.io/v1alpha3"},
		{kind: "UDPRoute", apiVersion: "gateway.networking.k8s.io/v1alpha2"},
	}

	for _, tt := range tests {
		t.Run(tt.kind+" "+tt.apiVersion, func(t *testing.T) {
			result := convertBinding(t, map[string]any{
				"apiVersion": tt.apiVersion,
				"kind":       tt.kind,
				"metadata":   map[string]any{"name": "route", "namespace": "default"},
				"spec": map[string]any{
					"parentRefs": []any{map[string]any{"name": "edge", "sectionName": "l4"}},
					"rules": []any{map[string]any{
						"backendRefs": []any{map[string]any{"name": "backend", "port": int64(5353)}},
					}},
				},
				"status": map[string]any{
					"parents": []any{map[string]any{
						"parentRef":      map[string]any{"name": "edge", "sectionName": "l4"},
						"controllerName": "example.com/controller",
						"conditions":     []any{map[string]any{"type": "Accepted", "status": "True", "reason": "Accepted"}},
					}},
				},
			})

			parentRef := &pb.KubernetesGatewayRouteData_ParentReference{Name: "edge", SectionName: new("l4")}

			assert.Equal(t, &pb.KubernetesGatewayRouteData{
				ParentRefs:  []*pb.KubernetesGatewayRouteData_ParentReference{parentRef},
				BackendRefs: []*pb.KubernetesGatewayRouteData_BackendRef{{Name: "backend", Port: new(uint32(5353))}},
				Parents: []*pb.KubernetesGatewayRouteData_RouteParentStatus{{
					ParentRef:      parentRef,
					ControllerName: "example.com/controller",
					Accepted:       new(true),
					AcceptedReason: new("Accepted"),
				}},
			}, result.GetGatewayRoute())
		})
	}
}

func TestConvertEntryPoint_Malformed(t *testing.T) {
	tests := map[string]map[string]any{
		"Gateway with a non-numeric listener port": {
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "Gateway",
			"metadata":   map[string]any{"name": "edge", "namespace": "default"},
			"spec": map[string]any{
				"listeners": []any{map[string]any{"name": "http", "port": "eighty", "protocol": "HTTP"}},
			},
		},
		"HTTPRoute with a non-list parentRefs field": {
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata":   map[string]any{"name": "edge", "namespace": "default"},
			"spec":       map[string]any{"parentRefs": "edge"},
		},
		"Ingress with a non-list rules field": {
			"apiVersion": "networking.k8s.io/v1",
			"kind":       "Ingress",
			"metadata":   map[string]any{"name": "edge", "namespace": "default"},
			"spec":       map[string]any{"rules": "not-a-list"},
		},
	}

	for name, obj := range tests {
		t.Run(name, func(t *testing.T) {
			core, logs := observer.New(zap.WarnLevel)

			result := convertBindingWithLogger(t, obj, zap.New(core))

			// A malformed object is still sent, with metadata only, and the reason is logged.
			assert.Equal(t, "edge", result.GetName())
			assert.Nil(t, result.GetKindSpecific())

			entries := logs.FilterMessage("Failed to read entry point, sending metadata only").All()
			require.Len(t, entries, 1)
			assert.Equal(t, obj["kind"], entries[0].ContextMap()["kind"])
			assert.Equal(t, "default", entries[0].ContextMap()["namespace"])
			assert.Equal(t, "edge", entries[0].ContextMap()["name"])
		})
	}
}

// A kind name alone must not select a converter: config.openshift.io serves an
// Ingress kind and networking.istio.io serves a Gateway kind, with other schemas.
func TestConvertEntryPoint_WrongGroup(t *testing.T) {
	tests := []struct {
		apiVersion string
		kinds      []string
	}{
		{apiVersion: "config.openshift.io/v1", kinds: []string{"Ingress"}},
		{apiVersion: "networking.istio.io/v1", kinds: []string{"Gateway"}},
		{apiVersion: "example.com/v1", kinds: []string{
			"Ingress", "IngressClass", "Gateway", "GatewayClass",
			"HTTPRoute", "GRPCRoute", "TCPRoute", "TLSRoute", "UDPRoute",
		}},
	}

	for _, tt := range tests {
		for _, kind := range tt.kinds {
			result := convertBinding(t, map[string]any{
				"apiVersion": tt.apiVersion,
				"kind":       kind,
				"metadata":   map[string]any{"name": "x", "namespace": "default"},
				"spec": map[string]any{
					"controller":       "example.com/controller",
					"controllerName":   "example.com/controller",
					"gatewayClassName": "eg",
					"parentRefs":       []any{map[string]any{"name": "edge"}},
				},
			})

			require.NotNil(t, result)
			assert.Nil(t, result.GetKindSpecific(), "%s %s", tt.apiVersion, kind)
		}
	}
}

func TestInt32PtrToUint32(t *testing.T) {
	assert.Nil(t, int32PtrToUint32(nil))
	assert.Nil(t, int32PtrToUint32(new(int32(-1))))
	assert.Equal(t, new(uint32(8080)), int32PtrToUint32(new(int32(8080))))
}
