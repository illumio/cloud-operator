// Copyright 2026 Illumio, Inc. All Rights Reserved.

package calicoenterprise

import (
	"bufio"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/collector"
	"github.com/illumio/cloud-operator/internal/pkg/tls"
)

// logCollectorLine wraps a JSON body in the syslog header that Calico
// Enterprise's LogCollector (fluentd) emits.
func logCollectorLine(body string) string {
	return `<14>Oct 26 21:10:09 calico-ent tigera_secure: ` + body
}

// fluentBitLine wraps a JSON body in the RFC 5424 syslog header that the
// Fluent Bit LogCollector (Tigera operator v1.44+) sends: version, timestamp, host, app name,
// empty procid, msgid and structured data, then a UTF-8 BOM before the message.
func fluentBitLine(body string) string {
	return "<14>1 2026-10-26T21:10:09.000000Z calico-ent tigera_secure - - - \xef\xbb\xbf" + body
}

func TestParseLogCollectorFlowSyslogFormats(t *testing.T) {
	body := `{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":59106,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`
	expected := Flow{
		SourceIP:        "10.244.163.79",
		DestinationIP:   "10.244.163.65",
		SourcePort:      59106,
		DestinationPort: 53,
		Protocol:        collector.UDP,
		IPVersion:       collector.IPv4,
		StartTimestamp:  timestamppb.New(time.Unix(1793048988, 0)),
	}

	tests := map[string]struct {
		line string
		err  error
	}{
		"Fluentd RFC 3164 record (Tigera operator v1.43 and earlier)": {
			line: logCollectorLine(body),
		},
		"Fluent Bit RFC 5424 record with a BOM (Tigera operator v1.44+)": {
			line: fluentBitLine(body),
		},
		"RFC 5424 record without a BOM": {
			line: "<14>1 2026-10-26T21:10:09.000000Z calico-ent tigera_secure - - - " + body,
		},
		"RFC 5424 record with a process ID": {
			line: "<14>1 2026-10-26T21:10:09.000000Z calico-ent tigera_secure 42 - - " + body,
		},
		"tigera_secure inside another app's message is not a LogCollector record": {
			line: `<14>1 2026-10-26T21:10:09.000000Z calico-ent other-app - - - {"note":" tigera_secure "}`,
			err:  ErrNotLogCollectorRecord,
		},
		"tag without a JSON body is not a LogCollector record": {
			line: "<14>1 2026-10-26T21:10:09.000000Z calico-ent tigera_secure - - - starting",
			err:  ErrNotLogCollectorRecord,
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
			assertFlow(t, expected, flow)
		})
	}
}

func TestParseLogCollectorFlow(t *testing.T) {
	tests := map[string]struct {
		line     string
		expected Flow
		err      error
	}{
		"TCP flow": {
			line: logCollectorLine(`{"start_time":1793049004,"end_time":1793049019,"source_ip":"10.244.163.75","source_port":40208,"dest_ip":"10.96.0.1","dest_port":443,"proto":"tcp","action":"allow","reporter":"src"}`),
			expected: Flow{
				SourceIP:        "10.244.163.75",
				DestinationIP:   "10.96.0.1",
				SourcePort:      40208,
				DestinationPort: 443,
				Protocol:        collector.TCP,
				IPVersion:       collector.IPv4,
				StartTimestamp:  timestamppb.New(time.Unix(1793049004, 0)),
			},
		},
		"UDP flow": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":59106,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			expected: Flow{
				SourceIP:        "10.244.163.79",
				DestinationIP:   "10.244.163.65",
				SourcePort:      59106,
				DestinationPort: 53,
				Protocol:        collector.UDP,
				IPVersion:       collector.IPv4,
				StartTimestamp:  timestamppb.New(time.Unix(1793048988, 0)),
			},
		},
		"IPv6 flow infers ipv6": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"fd00::1","source_port":51000,"dest_ip":"fd00::2","dest_port":443,"proto":"tcp"}`),
			expected: Flow{
				SourceIP:        "fd00::1",
				DestinationIP:   "fd00::2",
				SourcePort:      51000,
				DestinationPort: 443,
				Protocol:        collector.TCP,
				IPVersion:       collector.IPv6,
				StartTimestamp:  timestamppb.New(time.Unix(1793048988, 0)),
			},
		},
		"ICMP flow with zero ports": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":0,"dest_ip":"10.244.163.65","dest_port":0,"proto":"icmp"}`),
			expected: Flow{
				SourceIP:       "10.244.163.79",
				DestinationIP:  "10.244.163.65",
				Protocol:       collector.ICMP,
				IPVersion:      collector.IPv4,
				StartTimestamp: timestamppb.New(time.Unix(1793048988, 0)),
			},
		},
		"missing start_time is rejected": {
			line: logCollectorLine(`{"source_ip":"10.244.163.79","source_port":59106,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			err:  ErrMissingTimestamp,
		},
		"zero start_time is rejected": {
			line: logCollectorLine(`{"start_time":0,"source_ip":"10.244.163.79","source_port":59106,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			err:  ErrMissingTimestamp,
		},
		"ICMP flow with null ports": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":null,"dest_ip":"10.244.163.65","dest_port":null,"proto":"icmp"}`),
			expected: Flow{
				SourceIP:       "10.244.163.79",
				DestinationIP:  "10.244.163.65",
				Protocol:       collector.ICMP,
				IPVersion:      collector.IPv4,
				StartTimestamp: timestamppb.New(time.Unix(1793048988, 0)),
			},
		},
		"other syslog record is not a LogCollector record": {
			line: `<14>2026-06-29T22:45:59Z minikube calico-felix[51097]: INFO <nil> <nil>:  action="allow" dstIP="10.244.120.74" dstPort="8080" proto="6" srcIP="10.244.120.78" srcPort="48710" updateType=report`,
			err:  ErrNotLogCollectorRecord,
		},
		"malformed JSON is rejected": {
			line: logCollectorLine(`{"source_ip":"10.244.163.79",`),
			err:  ErrMalformedRecord,
		},
		"null source port (aggregated flow) is rejected as aggregated": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":null,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			err:  ErrAggregatedFlow,
		},
		"missing source port is rejected as aggregated": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","dest_ip":"10.244.163.65","dest_port":53,"proto":"tcp"}`),
			err:  ErrAggregatedFlow,
		},
		"missing dest_ip is incomplete": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":1,"dest_port":53,"proto":"udp"}`),
			err:  ErrIncompleteFlow,
		},
		"aggregated source_ip is rejected as aggregated": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"-","source_port":null,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			err:  ErrAggregatedFlow,
		},
		"aggregated dest_ip is rejected as aggregated": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":59106,"dest_ip":"-","dest_port":53,"proto":"udp"}`),
			err:  ErrAggregatedFlow,
		},
		"unknown protocol is rejected": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":1,"dest_ip":"10.244.163.65","dest_port":2,"proto":"gre"}`),
			err:  ErrUnknownProto,
		},
		"out of range port is rejected": {
			line: logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":70000,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			err:  ErrInvalidPort,
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
			assertFlow(t, tt.expected, flow)
		})
	}
}

func TestParseFlow(t *testing.T) {
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
			expectedError: ErrNotLogCollectorRecord,
		},
		"aggregated record is rejected": {
			line:          logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":null,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			expectedError: ErrAggregatedFlow,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			flow, err := ParseFlow(tt.line)
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
			wantErr: ErrNotLogCollectorRecord,
		},
		"aggregated LogCollector record is rejected": {
			line:    logCollectorLine(`{"start_time":1793048988,"source_ip":"10.244.163.79","source_port":null,"dest_ip":"10.244.163.65","dest_port":53,"proto":"udp"}`),
			wantErr: ErrAggregatedFlow,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			sink := newRecordingSink()
			collector := NewCollector(zap.NewNop(), "0", tls.ServerSettings{}, sink, nil)

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
	f, err := os.Open("testdata/logcollector_records.txt")
	require.NoError(t, err)

	t.Cleanup(func() { _ = f.Close() })

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), calicoSyslogMaxLineLength)

	var total int

	for scanner.Scan() {
		total++

		// The captured records come from Fluentd. Fluent Bit (operator v1.44+) sends the
		// same JSON body with an RFC 5424 header, so check that form too.
		_, body, found := strings.Cut(scanner.Text(), "tigera_secure: ")
		require.True(t, found, "record %d", total)

		_, err := ParseLogCollectorFlow(fluentBitLine(body))
		require.NoError(t, err, "record %d as RFC 5424", total)

		flow, err := ParseLogCollectorFlow(scanner.Text())
		require.NoError(t, err, "record %d", total)
		assert.NotEmpty(t, flow.SourceIP, "record %d", total)
		assert.NotEmpty(t, flow.DestinationIP, "record %d", total)
		assert.Contains(t, []string{collector.TCP, collector.UDP}, flow.Protocol, "record %d", total)
		assert.NotNil(t, flow.StartTimestamp, "record %d", total)

		fiveTupleFlow, err := ParseFlow(scanner.Text())
		require.NoError(t, err, "record %d", total)
		assert.Equal(t, flow.SourceIP, fiveTupleFlow.GetLayer3().GetSource(), "record %d", total)
		assert.Equal(t, flow.DestinationIP, fiveTupleFlow.GetLayer3().GetDestination(), "record %d", total)
	}

	require.NoError(t, scanner.Err())
	require.Equal(t, 20, total)
}

func assertFlow(t *testing.T, expected, actual Flow) {
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
