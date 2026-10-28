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
	"google.golang.org/protobuf/proto"
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
		"missing start_time is rejected": {
			line: logCollectorLine(`{"source_ip":"10.244.163.79","source_port":59106,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			err:  ErrCalicoMissingTimestamp,
		},
		"zero start_time is rejected": {
			line: logCollectorLine(`{"start_time":0,"source_ip":"10.244.163.79","source_port":59106,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			err:  ErrCalicoMissingTimestamp,
		},
		"ICMP flow with null ports": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":null,"dest_ip":"10.244.163.65","dest_port":null,"proto":"icmp"}`),
			expected: CalicoFlow{
				SourceIP:       "10.244.163.79",
				DestinationIP:  "10.244.163.65",
				Protocol:       ICMP,
				IPVersion:      IPv4,
				StartTimestamp: timestamppb.New(time.Unix(1793048988, 0)),
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
		"null source port (aggregated flow) is rejected as aggregated": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":null,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			err:  ErrCalicoAggregatedFlow,
		},
		"missing source port is rejected as aggregated": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","dest_ip":"10.244.163.65","dest_port":53,"proto":"tcp"}`),
			err:  ErrCalicoAggregatedFlow,
		},
		"missing dest_ip is incomplete": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":1,"dest_port":53,"proto":"udp"}`),
			err:  ErrCalicoIncompleteFlow,
		},
		"aggregated source_ip is rejected as aggregated": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"-","source_port":null,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			err:  ErrCalicoAggregatedFlow,
		},
		"aggregated dest_ip is rejected as aggregated": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":59106,"dest_ip":"-","dest_port":53,"proto":"udp"}`),
			err:  ErrCalicoAggregatedFlow,
		},
		"unknown protocol is rejected": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":1,"dest_ip":"10.244.163.65","dest_port":2,"proto":"gre"}`),
			err:  ErrCalicoUnknownProto,
		},
		"out of range port is rejected": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":70000,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
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

func TestParseCalicoEnterpriseFlow(t *testing.T) {
	policies := `"policies":{"enforced_policies":["0|calico-system|np:kube-system/calico-system.cluster-dns|allow|0"]}`

	udpFlow := func(sourceIP, destinationIP string, ipVersion pb.IPVersion) *pb.FiveTupleFlow {
		return &pb.FiveTupleFlow{
			Layer3: &pb.IP{Source: sourceIP, Destination: destinationIP, IpVersion: ipVersion},
			Layer4: &pb.Layer4{Protocol: &pb.Layer4_Udp{Udp: &pb.UDP{SourcePort: 59106, DestinationPort: 53}}},
			Ts:     &pb.FiveTupleFlow_Timestamp{Timestamp: timestamppb.New(time.Unix(1793048988, 0))},
		}
	}

	tests := map[string]struct {
		line          string
		expected      *pb.FiveTupleFlow
		expectedError error
	}{
		"source side record sends only the five-tuple": {
			line:     logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":59106,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp","action":"allow","reporter":"src",` + policies + `}`),
			expected: udpFlow("10.244.163.79", "10.244.163.65", pb.IPVersion_IP_VERSION_IPV4),
		},
		"denied destination side record sends only the five-tuple": {
			line:     logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":59106,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp","action":"deny","reporter":"dst",` + policies + `}`),
			expected: udpFlow("10.244.163.79", "10.244.163.65", pb.IPVersion_IP_VERSION_IPV4),
		},
		"IPv6 record": {
			line:     logCollectorLine(`{"start_time":1793048988,"source_ip":"fd00::1","source_port":59106,"dest_ip":"fd00::2","dest_port":53,"proto":"udp"}`),
			expected: udpFlow("fd00::1", "fd00::2", pb.IPVersion_IP_VERSION_IPV6),
		},
		"other syslog record is not a LogCollector record": {
			line:          `<14>2026-06-29T22:45:59Z minikube calico-felix[51097]: INFO <nil> <nil>:  action="allow" dstIP="10.244.120.74" dstPort="8080" proto="6" srcIP="10.244.120.78" srcPort="48710" updateType=report`,
			expectedError: ErrCalicoNotLogCollectorRecord,
		},
		"aggregated record is rejected": {
			line:          logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":null,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			expectedError: ErrCalicoAggregatedFlow,
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
			assert.True(t, proto.Equal(tt.expected, flow), "got %v", flow)
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
			line:     logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":1,"dest_ip":"10.244.163.65","dest_port":2,"proto":"tcp","action":"allow","reporter":"src"}`),
			wantType: &pb.FiveTupleFlow{},
		},
		"Felix flow record is rejected": {
			line:    `<14>2026-06-29T22:45:59Z minikube calico-felix[51097]: INFO <nil> <nil>:  action="allow" dstIP="10.244.120.74" dstPort="8080" proto="6" srcIP="10.244.120.78" srcPort="48710" updateType=report`,
			wantErr: ErrCalicoNotLogCollectorRecord,
		},
		"aggregated LogCollector record is rejected": {
			line:    logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":null,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			wantErr: ErrCalicoAggregatedFlow,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			sink := newRecordingSink()
			collector := NewCalicoCollector(zap.NewNop(), "0", CalicoSyslogTLS{}, sink, nil)

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

		fiveTupleFlow, err := ParseCalicoEnterpriseFlow(scanner.Text())
		require.NoError(t, err, "record %d", total)
		assert.Equal(t, flow.SourceIP, fiveTupleFlow.GetLayer3().GetSource(), "record %d", total)
		assert.Equal(t, flow.DestinationIP, fiveTupleFlow.GetLayer3().GetDestination(), "record %d", total)
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
