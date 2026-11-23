// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// openShiftNetworkGVR identifies OpenShift's cluster network configuration.
// Its spec.exportNetworkFlows.ipfix.collectors lists the addresses to which
// OVN-Kubernetes exports network flows.
var openShiftNetworkGVR = schema.GroupVersionResource{
	Group:    "operator.openshift.io",
	Version:  "v1",
	Resource: "networks",
}

const (
	// openShiftNetworkName is the name of the only OpenShift network configuration.
	openShiftNetworkName = "cluster"

	// ipfixExportCheckPeriod is the period between two checks of the IPFIX export.
	ipfixExportCheckPeriod = 5 * time.Minute
)

// ipfixExportState is the outcome of a check of the IPFIX export.
type ipfixExportState int

const (
	// ipfixExportUnknown means that the export could not be checked this time.
	ipfixExportUnknown ipfixExportState = iota
	// ipfixExportUncheckable means that the export can never be checked: the
	// cluster is not OpenShift, or the operator may not read its configuration.
	ipfixExportUncheckable
	// ipfixExportNoService means that no Service leads to the IPFIX collector.
	ipfixExportNoService
	// ipfixExportNotConfigured means that OVN-Kubernetes exports no flows.
	ipfixExportNotConfigured
	// ipfixExportElsewhere means that OVN-Kubernetes exports flows only to
	// addresses that do not lead to the IPFIX collector.
	ipfixExportElsewhere
	// ipfixExportOK means that OVN-Kubernetes exports flows to the IPFIX collector.
	ipfixExportOK
)

// IPFIXExportChecker checks that OVN-Kubernetes on OpenShift exports its
// network flows to the operator's IPFIX collector.
//
// Open vSwitch sends flows to a fixed IP address: the cluster IP that the
// operator's Service had when the export was configured. If the Service is
// created again or renamed, it gets another cluster IP and the flows are sent
// to an address where nothing listens. The operator then receives no flows,
// with no other sign of the cause. The checker only reports the problem: it
// never changes the cluster's network configuration.
type IPFIXExportChecker struct {
	logger        *zap.Logger
	clientset     kubernetes.Interface
	dynamicClient dynamic.Interface
	namespace     string
	podName       string
	collectorPort int32

	lastState      ipfixExportState
	lastConfigured []string
	lastExpected   []string
}

// NewIPFIXExportChecker creates a checker for the operator's pod podName in
// namespace, whose IPFIX collector listens on collectorPort.
func NewIPFIXExportChecker(logger *zap.Logger, clientset kubernetes.Interface, dynamicClient dynamic.Interface, namespace, podName, collectorPort string) (*IPFIXExportChecker, error) {
	port, err := strconv.ParseInt(collectorPort, 10, 32)
	if err != nil {
		return nil, err
	}

	return &IPFIXExportChecker{
		logger:        logger,
		clientset:     clientset,
		dynamicClient: dynamicClient,
		namespace:     namespace,
		podName:       podName,
		collectorPort: int32(port),
	}, nil
}

// Run checks the IPFIX export now and then periodically, until the context is
// canceled or the export turns out to be uncheckable.
func (c *IPFIXExportChecker) Run(ctx context.Context) {
	ticker := time.NewTicker(ipfixExportCheckPeriod)
	defer ticker.Stop()

	for {
		if c.check(ctx) == ipfixExportUncheckable {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// check checks the IPFIX export once. It logs the outcome only if it differs
// from the outcome of the previous check, so a lasting problem is logged once.
func (c *IPFIXExportChecker) check(ctx context.Context) ipfixExportState {
	state, configured, expected := c.exportState(ctx)

	if state == c.lastState && slices.Equal(configured, c.lastConfigured) && slices.Equal(expected, c.lastExpected) {
		return state
	}

	c.lastState, c.lastConfigured, c.lastExpected = state, configured, expected

	logger := c.logger.With(
		zap.Strings("configured_collectors", configured),
		zap.Strings("expected_collectors", expected),
	)

	switch state {
	case ipfixExportNoService:
		logger.Warn("No Service leads to the IPFIX collector of this pod, so OVN-Kubernetes cannot export network flows to it",
			zap.Int32("ipfix_collector_port", c.collectorPort))
	case ipfixExportNotConfigured:
		logger.Error("OVN-Kubernetes does not export network flows, so no flows will be received; " +
			"set spec.exportNetworkFlows.ipfix.collectors of network.operator.openshift.io/cluster to one of the expected collectors")
	case ipfixExportElsewhere:
		logger.Error("OVN-Kubernetes exports network flows to an address that does not lead to this pod, so no flows will be received; " +
			"set spec.exportNetworkFlows.ipfix.collectors of network.operator.openshift.io/cluster to one of the expected collectors")
	case ipfixExportOK:
		logger.Info("OVN-Kubernetes exports network flows to the IPFIX collector")
	case ipfixExportUnknown, ipfixExportUncheckable:
		// Already logged with the error that caused it.
	}

	return state
}

// exportState compares the collectors that OVN-Kubernetes exports flows to with
// the addresses of the Services that lead to the operator's IPFIX collector.
func (c *IPFIXExportChecker) exportState(ctx context.Context) (ipfixExportState, []string, []string) {
	network, err := c.dynamicClient.Resource(openShiftNetworkGVR).Get(ctx, openShiftNetworkName, metav1.GetOptions{})

	switch {
	case apierrors.IsNotFound(err):
		c.logger.Debug("No OpenShift network configuration found, so the IPFIX export of OVN-Kubernetes is not checked")

		return ipfixExportUncheckable, nil, nil
	case apierrors.IsForbidden(err):
		c.logger.Warn("Not allowed to read network.operator.openshift.io/cluster, so the IPFIX export of OVN-Kubernetes is not checked",
			zap.Error(err))

		return ipfixExportUncheckable, nil, nil
	case err != nil:
		c.logger.Warn("Failed to read network.operator.openshift.io/cluster to check the IPFIX export of OVN-Kubernetes; will retry",
			zap.Error(err))

		return ipfixExportUnknown, nil, nil
	}

	expected, err := c.collectorAddresses(ctx)
	if apierrors.IsNotFound(err) {
		c.logger.Warn("Failed to find this pod, so the IPFIX export of OVN-Kubernetes is not checked",
			zap.String("pod", c.podName), zap.Error(err))

		return ipfixExportUncheckable, nil, nil
	}

	if err != nil {
		c.logger.Warn("Failed to find the Services of the IPFIX collector to check the IPFIX export of OVN-Kubernetes; will retry",
			zap.Error(err))

		return ipfixExportUnknown, nil, nil
	}

	// A missing or malformed field means that no collector is configured.
	configured, _, _ := unstructured.NestedStringSlice(network.Object, "spec", "exportNetworkFlows", "ipfix", "collectors")

	switch {
	case len(expected) == 0:
		return ipfixExportNoService, configured, nil
	case len(configured) == 0:
		return ipfixExportNotConfigured, nil, expected
	case !slices.ContainsFunc(configured, func(collector string) bool {
		return slices.Contains(expected, normalizeAddress(collector))
	}):
		return ipfixExportElsewhere, configured, expected
	}

	return ipfixExportOK, configured, expected
}

// collectorAddresses returns the addresses, as IP:port, at which the Services
// in the operator's namespace expose the IPFIX collector of the operator's pod.
//
// Only Services that select the pod count: a Service left behind by an earlier
// installation still has a cluster IP, but nothing listens behind it.
func (c *IPFIXExportChecker) collectorAddresses(ctx context.Context) ([]string, error) {
	pod, err := c.clientset.CoreV1().Pods(c.namespace).Get(ctx, c.podName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	services, err := c.clientset.CoreV1().Services(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var addresses []string

	for _, service := range services.Items {
		// A Service with no selector selects no pod.
		if len(service.Spec.Selector) == 0 || !labels.SelectorFromSet(service.Spec.Selector).Matches(labels.Set(pod.Labels)) {
			continue
		}

		for _, port := range service.Spec.Ports {
			if !c.isCollectorPort(port) {
				continue
			}

			for _, clusterIP := range serviceClusterIPs(service) {
				addresses = append(addresses, net.JoinHostPort(clusterIP, strconv.Itoa(int(port.Port))))
			}
		}
	}

	slices.Sort(addresses)

	return slices.Compact(addresses), nil
}

// isCollectorPort reports whether the Service port forwards UDP to the port on
// which the IPFIX collector listens.
func (c *IPFIXExportChecker) isCollectorPort(port corev1.ServicePort) bool {
	if port.Protocol != corev1.ProtocolUDP {
		return false
	}

	// A named target port is not resolved: the chart uses the port number.
	targetPort := port.TargetPort.IntVal
	if targetPort == 0 && port.TargetPort.StrVal == "" {
		// The target port defaults to the Service port.
		targetPort = port.Port
	}

	return targetPort == c.collectorPort
}

// serviceClusterIPs returns the cluster IPs of the Service, in canonical form.
// A headless Service has none.
func serviceClusterIPs(service corev1.Service) []string {
	clusterIPs := service.Spec.ClusterIPs
	if len(clusterIPs) == 0 {
		clusterIPs = []string{service.Spec.ClusterIP}
	}

	var ips []string

	for _, clusterIP := range clusterIPs {
		ip, err := netip.ParseAddr(clusterIP)
		if err != nil {
			// "None" for a headless Service, or no cluster IP assigned yet.
			continue
		}

		ips = append(ips, ip.String())
	}

	return ips
}

// normalizeAddress returns the IP:port address in canonical form, so that two
// spellings of one IPv6 address compare equal. An address that cannot be parsed
// is returned unchanged.
func normalizeAddress(address string) string {
	addrPort, err := netip.ParseAddrPort(address)
	if err != nil {
		return address
	}

	return addrPort.String()
}
