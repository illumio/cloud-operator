// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"bufio"
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

// logCollectorLine wraps a JSON body in the syslog header that Calico
// Enterprise's LogCollector (fluentd) emits.
func logCollectorLine(body string) string {
	return `<14>Oct 26 21:10:09 calico-ent tigera_secure: ` + body
}

func TestParseLogCollectorFlow(t *testing.T) {
	tests := map[string]struct {
		line     string
		expected CalicoFlow
		err      error
	}{
		"TCP flow": {
			line: logCollectorLine(`{"start_time":1793049004,"end_time":1793049019,"source_ip":"10.244.163.75","source_port":40208,"dest_ip":"10.96.0.1","dest_port":443,"proto":"tcp","action":"allow","reporter":"src"}`),
			expected: CalicoFlow{
				SourceIP:        "10.244.163.75",
				DestinationIP:   "10.96.0.1",
				SourcePort:      40208,
				DestinationPort: 443,
				Protocol:        TCP,
				IPVersion:       IPv4,
				StartTimestamp:  timestamppb.New(time.Unix(1793049004, 0)),
			},
		},
		"UDP flow": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":59106,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			expected: CalicoFlow{
				SourceIP:        "10.244.163.79",
				DestinationIP:   "10.244.163.65",
				SourcePort:      59106,
				DestinationPort: 53,
				Protocol:        UDP,
				IPVersion:       IPv4,
				StartTimestamp:  timestamppb.New(time.Unix(1793048988, 0)),
			},
		},
		"IPv6 flow infers ipv6": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"fd00::1","source_port":51000,"dest_ip":"fd00::2","dest_port":443,"proto":"tcp"}`),
			expected: CalicoFlow{
				SourceIP:        "fd00::1",
				DestinationIP:   "fd00::2",
				SourcePort:      51000,
				DestinationPort: 443,
				Protocol:        TCP,
				IPVersion:       IPv6,
				StartTimestamp:  timestamppb.New(time.Unix(1793048988, 0)),
			},
		},
		"ICMP flow with zero ports": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":0,"dest_ip":"10.244.163.65","dest_port":0,"proto":"icmp"}`),
			expected: CalicoFlow{
				SourceIP:       "10.244.163.79",
				DestinationIP:  "10.244.163.65",
				Protocol:       ICMP,
				IPVersion:      IPv4,
				StartTimestamp: timestamppb.New(time.Unix(1793048988, 0)),
			},
		},
		"missing start_time leaves timestamp nil": {
			line: logCollectorLine(`{"source_ip":"10.244.163.79","source_port":59106,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			expected: CalicoFlow{
				SourceIP:        "10.244.163.79",
				DestinationIP:   "10.244.163.65",
				SourcePort:      59106,
				DestinationPort: 53,
				Protocol:        UDP,
				IPVersion:       IPv4,
			},
		},
		"other syslog record is not a LogCollector record": {
			line: `<14>2026-06-29T22:45:59Z minikube calico-felix[51097]: INFO <nil> <nil>:  action="allow" dstIP="10.244.120.74" dstPort="8080" proto="6" srcIP="10.244.120.78" srcPort="48710" updateType=report`,
			err:  ErrCalicoNotLogCollectorRecord,
		},
		"malformed JSON is rejected": {
			line: logCollectorLine(`{"source_ip":"10.244.163.79",`),
			err:  ErrCalicoMalformedRecord,
		},
		"null source port (aggregated flow) is incomplete": {
			line: logCollectorLine(`{"source_ip":"10.244.163.79","source_port":null,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			err:  ErrCalicoIncompleteFlow,
		},
		"missing dest_ip is incomplete": {
			line: logCollectorLine(`{"source_ip":"10.244.163.79","source_port":1,"dest_port":53,"proto":"udp"}`),
			err:  ErrCalicoIncompleteFlow,
		},
		"aggregated source_ip is rejected": {
			line: logCollectorLine(`{"source_ip":"-","source_port":59106,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			err:  ErrCalicoInvalidIP,
		},
		"unknown protocol is rejected": {
			line: logCollectorLine(`{"source_ip":"10.244.163.79","source_port":1,"dest_ip":"10.244.163.65","dest_port":2,"proto":"gre"}`),
			err:  ErrCalicoUnknownProto,
		},
		"out of range port is rejected": {
			line: logCollectorLine(`{"source_ip":"10.244.163.79","source_port":70000,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			err:  ErrCalicoInvalidPort,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			flow, err := ParseLogCollectorFlow(tt.line)
			if tt.err != nil {
				assert.ErrorIs(t, err, tt.err)

				return
			}

			require.NoError(t, err)
			assertCalicoFlow(t, tt.expected, flow)
		})
	}
}

func TestParseCalicoPolicy(t *testing.T) {
	tests := map[string]struct {
		value    string
		policy   *pb.Policy
		action   string
		parsedOK bool
	}{
		"namespaced Calico NetworkPolicy": {
			value:    "0|calico-system|np:kube-system/calico-system.cluster-dns|allow|0",
			policy:   &pb.Policy{Name: "calico-system.cluster-dns", Namespace: "kube-system", Kind: "NetworkPolicy"},
			action:   "allow",
			parsedOK: true,
		},
		"GlobalNetworkPolicy has no namespace": {
			value:    "1|security|gnp:security.block-egress|deny|3",
			policy:   &pb.Policy{Name: "security.block-egress", Kind: "GlobalNetworkPolicy"},
			action:   "deny",
			parsedOK: true,
		},
		"Kubernetes NetworkPolicy": {
			value:    "2|default|knp:shop/knp.default.allow-web|allow|0",
			policy:   &pb.Policy{Name: "knp.default.allow-web", Namespace: "shop", Kind: "KubernetesNetworkPolicy"},
			action:   "allow",
			parsedOK: true,
		},
		"unknown kind prefix is kept as is": {
			value:    "0|default|xyz:shop/policy-a|allow|0",
			policy:   &pb.Policy{Name: "policy-a", Namespace: "shop", Kind: "xyz"},
			action:   "allow",
			parsedOK: true,
		},
		"policy without a kind prefix": {
			value:    "0|default|shop/default.policy-a|deny|1",
			policy:   &pb.Policy{Name: "default.policy-a", Namespace: "shop"},
			action:   "deny",
			parsedOK: true,
		},
		"too few fields is rejected": {
			value: "0|default|np:shop/policy-a",
		},
		"empty name is rejected": {
			value: "0|default|np:shop/|allow|0",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			policy, action, ok := parseCalicoPolicy(tt.value)
			require.Equal(t, tt.parsedOK, ok)

			if !tt.parsedOK {
				return
			}

			assert.Equal(t, tt.action, action)
			assert.Equal(t, tt.policy.GetName(), policy.GetName())
			assert.Equal(t, tt.policy.GetNamespace(), policy.GetNamespace())
			assert.Equal(t, tt.policy.GetKind(), policy.GetKind())
		})
	}
}

func TestParseCalicoEnterpriseFlow(t *testing.T) {
	allowDNS := "0|calico-system|np:kube-system/calico-system.cluster-dns|allow|0"
	denyAll := "1|security|gnp:security.default-deny|deny|0"

	tests := map[string]struct {
		line          string
		direction     pb.TrafficDirection
		verdict       pb.Verdict
		egressAllow   []string
		ingressAllow  []string
		egressDeny    []string
		ingressDeny   []string
		expectedError error
	}{
		"source side allowed": {
			line:        logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":59106,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp","action":"allow","reporter":"src","policies":{"enforced_policies":["` + allowDNS + `"],"pending_policies":["` + denyAll + `"]}}`),
			direction:   pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS,
			verdict:     pb.Verdict_VERDICT_FORWARDED,
			egressAllow: []string{"calico-system.cluster-dns"},
		},
		"destination side denied": {
			line:        logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":59106,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp","action":"deny","reporter":"dst","policies":{"enforced_policies":["` + denyAll + `"]}}`),
			direction:   pb.TrafficDirection_TRAFFIC_DIRECTION_INGRESS,
			verdict:     pb.Verdict_VERDICT_DROPPED,
			ingressDeny: []string{"security.default-deny"},
		},
		"destination side with allow and deny policies": {
			line:         logCollectorLine(`{"source_ip":"10.244.163.79","source_port":1,"dest_ip":"10.244.163.65","dest_port":2,"proto":"tcp","action":"allow","reporter":"dst","policies":{"enforced_policies":["` + denyAll + `","` + allowDNS + `","malformed"]}}`),
			direction:    pb.TrafficDirection_TRAFFIC_DIRECTION_INGRESS,
			verdict:      pb.Verdict_VERDICT_FORWARDED,
			ingressAllow: []string{"calico-system.cluster-dns"},
			ingressDeny:  []string{"security.default-deny"},
		},
		"unknown reporter and action are unspecified and carry no policies": {
			line: logCollectorLine(`{"source_ip":"10.244.163.79","source_port":1,"dest_ip":"10.244.163.65","dest_port":2,"proto":"tcp","action":"maybe","reporter":"other","policies":{"enforced_policies":["` + allowDNS + `"]}}`),
		},
		"other syslog record is not a LogCollector record": {
			line:          `<14>2026-06-29T22:45:59Z minikube calico-felix[51097]: INFO <nil> <nil>:  action="allow" dstIP="10.244.120.74" dstPort="8080" proto="6" srcIP="10.244.120.78" srcPort="48710" updateType=report`,
			expectedError: ErrCalicoNotLogCollectorRecord,
		},
		"incomplete record is rejected": {
			line:          logCollectorLine(`{"source_ip":"10.244.163.79","source_port":null,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp","action":"allow","reporter":"src"}`),
			expectedError: ErrCalicoIncompleteFlow,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			flow, err := ParseCalicoEnterpriseFlow(tt.line)
			if tt.expectedError != nil {
				assert.ErrorIs(t, err, tt.expectedError)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, "10.244.163.79", flow.GetLayer3().GetSource())
			assert.Equal(t, "10.244.163.65", flow.GetLayer3().GetDestination())
			assert.NotNil(t, flow.GetLayer4())
			assert.Equal(t, tt.direction, flow.GetTrafficDirection())
			assert.Equal(t, tt.verdict, flow.GetVerdict())
			assert.Equal(t, tt.egressAllow, policyNames(flow.GetEgressAllowedBy()))
			assert.Equal(t, tt.ingressAllow, policyNames(flow.GetIngressAllowedBy()))
			assert.Equal(t, tt.egressDeny, policyNames(flow.GetEgressDeniedBy()))
			assert.Equal(t, tt.ingressDeny, policyNames(flow.GetIngressDeniedBy()))
		})
	}
}

func TestCalicoProcessLineCachesFlowByFormat(t *testing.T) {
	tests := map[string]struct {
		line     string
		wantType pb.Flow
		wantErr  error
	}{
		"LogCollector record is sent as a FiveTupleFlow": {
			line:     logCollectorLine(`{"source_ip":"10.244.163.79","source_port":1,"dest_ip":"10.244.163.65","dest_port":2,"proto":"tcp","action":"allow","reporter":"src"}`),
			wantType: &pb.FiveTupleFlow{},
		},
		"Felix flow record is rejected": {
			line:    `<14>2026-06-29T22:45:59Z minikube calico-felix[51097]: INFO <nil> <nil>:  action="allow" dstIP="10.244.120.74" dstPort="8080" proto="6" srcIP="10.244.120.78" srcPort="48710" updateType=report`,
			wantErr: ErrCalicoNotLogCollectorRecord,
		},
		"broken LogCollector record is rejected": {
			line:    logCollectorLine(`{"source_ip":"10.244.163.79","source_port":null,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			wantErr: ErrCalicoIncompleteFlow,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			sink := newRecordingSink()
			collector := NewCalicoCollector(zap.NewNop(), "0", CalicoSyslogTLS{}, sink)

			err := collector.processLine(context.Background(), zap.NewNop(), tt.line)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Equal(t, 0, sink.count())

				return
			}

			require.NoError(t, err)
			require.Equal(t, 1, sink.count())
			assert.IsType(t, tt.wantType, sink.flows[0])
		})
	}
}

func policyNames(policies []*pb.Policy) []string {
	if len(policies) == 0 {
		return nil
	}

	names := make([]string, 0, len(policies))
	for _, policy := range policies {
		names = append(names, policy.GetName())
	}

	return names
}

// TestParseLogCollectorFlowCapturedRecords runs real records captured from a
// Calico Enterprise LogCollector syslog store through the parser.
func TestParseLogCollectorFlowCapturedRecords(t *testing.T) {
	f, err := os.Open("testdata/calico_logcollector_records.txt")
	require.NoError(t, err)

	t.Cleanup(func() { _ = f.Close() })

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), calicoSyslogMaxLineLength)

	var total int

	for scanner.Scan() {
		total++

		flow, err := ParseLogCollectorFlow(scanner.Text())
		require.NoError(t, err, "record %d", total)
		assert.NotEmpty(t, flow.SourceIP, "record %d", total)
		assert.NotEmpty(t, flow.DestinationIP, "record %d", total)
		assert.Contains(t, []string{TCP, UDP}, flow.Protocol, "record %d", total)
		assert.NotNil(t, flow.StartTimestamp, "record %d", total)

		// Every captured record was reported and allowed by an enforced policy.
		enterpriseFlow, err := ParseCalicoEnterpriseFlow(scanner.Text())
		require.NoError(t, err, "record %d", total)
		assert.Equal(t, pb.Verdict_VERDICT_FORWARDED, enterpriseFlow.GetVerdict(), "record %d", total)
		assert.NotEqual(t, pb.TrafficDirection_TRAFFIC_DIRECTION_TRAFFIC_DIRECTION_UNKNOWN_UNSPECIFIED, enterpriseFlow.GetTrafficDirection(), "record %d", total)
		assert.NotEmpty(t, append(enterpriseFlow.GetEgressAllowedBy(), enterpriseFlow.GetIngressAllowedBy()...), "record %d", total)
	}

	require.NoError(t, scanner.Err())
	require.Equal(t, 20, total)
}

func assertCalicoFlow(t *testing.T, expected, actual CalicoFlow) {
	t.Helper()

	assert.Equal(t, expected.SourceIP, actual.SourceIP)
	assert.Equal(t, expected.DestinationIP, actual.DestinationIP)
	assert.Equal(t, expected.SourcePort, actual.SourcePort)
	assert.Equal(t, expected.DestinationPort, actual.DestinationPort)
	assert.Equal(t, expected.Protocol, actual.Protocol)
	assert.Equal(t, expected.IPVersion, actual.IPVersion)

	if expected.StartTimestamp == nil {
		assert.Nil(t, actual.StartTimestamp)

		return
	}

	require.NotNil(t, actual.StartTimestamp)
	assert.True(t, expected.StartTimestamp.AsTime().Equal(actual.StartTimestamp.AsTime()))
}
