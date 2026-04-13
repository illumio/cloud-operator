// Copyright 2024 Illumio, Inc. All Rights Reserved.

package collector

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

func TestParsePodNetworkInfo(t *testing.T) {
	tests := map[string]struct {
		input    string
		expected *pb.FiveTupleFlow
		err      error
	}{
		"valid TCP flow": {
			input: "time=1987-02-22T00:39:07.267635635+0000\t srcip=192.168.1.1 dstip=192.168.1.2 srcport=1234 dstport=5678 proto=tcp ipversion=ipv4",
			expected: &pb.FiveTupleFlow{
				Layer3: &pb.IP{
					Source:      "192.168.1.1",
					Destination: "192.168.1.2",
					IpVersion:   pb.IPVersion_IP_VERSION_IPV4,
				},
				Layer4: &pb.Layer4{
					Protocol: &pb.Layer4_Tcp{
						Tcp: &pb.TCP{
							SourcePort:      1234,
							DestinationPort: 5678,
							Flags:           &pb.TCPFlags{},
						},
					},
				},
			},
			err: nil,
		},
		"valid UDP flow": {
			input: "time=1987-02-22T00:39:07.267635635+0000\t srcip=192.168.1.1 dstip=192.168.1.2 srcport=1234 dstport=5678 proto=udp ipversion=ipv4",
			expected: &pb.FiveTupleFlow{
				Layer3: &pb.IP{
					Source:      "192.168.1.1",
					Destination: "192.168.1.2",
					IpVersion:   pb.IPVersion_IP_VERSION_IPV4,
				},
				Layer4: &pb.Layer4{
					Protocol: &pb.Layer4_Udp{
						Udp: &pb.UDP{
							SourcePort:      1234,
							DestinationPort: 5678,
						},
					},
				},
			},
			err: nil,
		},
		"Incomplete L3 TCP flow": {
			input:    "time=1987-02-22T00:39:07.267635635+0000\t srcip=192.168.1.1 dstip=192.168.1.2 srcport=1234 dstport=5678 proto=udp ipversion=",
			expected: nil,
			err:      ErrFalcoIncompleteL3Flow,
		},
		"Incomplete L4 TCP flow": {
			input:    "time=1987-02-22T00:39:07.267635635+0000\t srcip=192.168.1.1 dstip=192.168.1.2 srcport=1234 dstport=5678 proto= ipversion=ipv4",
			expected: nil,
			err:      ErrFalcoIncompleteL4Flow,
		},
		"invalid input": {
			input:    "invalid=input",
			expected: nil,
			err:      ErrFalcoEventIsNotFlow,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := ParsePodNetworkInfo(tt.input)
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
			} else {
				require.NoError(t, err)
				// Compare Layer3 and Layer4 separately since timestamp varies
				assert.Equal(t, tt.expected.GetLayer3(), result.GetLayer3())
				assert.Equal(t, tt.expected.GetLayer4(), result.GetLayer4())
				assert.NotNil(t, result.GetTs())
			}
		})
	}
}

func TestFilterIllumioTraffic(t *testing.T) {
	tests := map[string]struct {
		input    string
		expected bool
	}{
		"contains Illumio traffic": {
			input:    "some text with illumio_network_traffic inside",
			expected: true,
		},
		"does not contain Illumio traffic": {
			input:    "some regular log message",
			expected: false,
		},
		"empty string": {
			input:    "",
			expected: false,
		},
		"Illumio traffic at start": {
			input:    "illumio_network_traffic some other text",
			expected: true,
		},
		"Illumio traffic at end": {
			input:    "some other text illumio_network_traffic",
			expected: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := FilterIllumioTraffic(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCreateLayer4Message(t *testing.T) {
	tests := map[string]struct {
		proto       string
		srcPort     uint32
		dstPort     uint32
		ipVersion   string
		expected    *pb.Layer4
		expectedErr error
	}{
		"TCP protocol": {
			proto:     TCP,
			srcPort:   80,
			dstPort:   8080,
			ipVersion: "",
			expected: &pb.Layer4{
				Protocol: &pb.Layer4_Tcp{
					Tcp: &pb.TCP{
						SourcePort:      80,
						DestinationPort: 8080,
						Flags:           &pb.TCPFlags{},
					},
				},
			},
			expectedErr: nil,
		},
		"UDP protocol": {
			proto:     UDP,
			srcPort:   123,
			dstPort:   456,
			ipVersion: "",
			expected: &pb.Layer4{
				Protocol: &pb.Layer4_Udp{
					Udp: &pb.UDP{
						SourcePort:      123,
						DestinationPort: 456,
					},
				},
			},
			expectedErr: nil,
		},
		"ICMP protocol with IPv4": {
			proto:     ICMP,
			srcPort:   0,
			dstPort:   0,
			ipVersion: IPv4,
			expected: &pb.Layer4{
				Protocol: &pb.Layer4_Icmpv4{
					Icmpv4: &pb.ICMPv4{},
				},
			},
			expectedErr: nil,
		},
		"ICMP protocol with IPv6": {
			proto:     ICMP,
			srcPort:   0,
			dstPort:   0,
			ipVersion: IPv6,
			expected: &pb.Layer4{
				Protocol: &pb.Layer4_Icmpv6{
					Icmpv6: &pb.ICMPv6{},
				},
			},
			expectedErr: nil,
		},
		"Unknown protocol": {
			proto:       "unknown",
			srcPort:     0,
			dstPort:     0,
			ipVersion:   "",
			expected:    nil,
			expectedErr: ErrFalcoIncompleteL4Flow,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := CreateLayer4Message(tt.proto, tt.srcPort, tt.dstPort, tt.ipVersion)
			if tt.expectedErr != nil {
				require.ErrorIs(t, err, tt.expectedErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestCreateLayer3Message(t *testing.T) {
	tests := map[string]struct {
		source        string
		destination   string
		ipVersion     string
		expected      *pb.IP
		expectedError error
	}{
		"IPv4": {
			source:      "192.168.0.1",
			destination: "192.168.0.2",
			ipVersion:   IPv4,
			expected: &pb.IP{
				Source:      "192.168.0.1",
				Destination: "192.168.0.2",
				IpVersion:   pb.IPVersion_IP_VERSION_IPV4,
			},
			expectedError: nil,
		},
		"IPv6": {
			source:      "fe80::1",
			destination: "fe80::2",
			ipVersion:   IPv6,
			expected: &pb.IP{
				Source:      "fe80::1",
				Destination: "fe80::2",
				IpVersion:   pb.IPVersion_IP_VERSION_IPV6,
			},
			expectedError: nil,
		},
		"Unspecified IP version": {
			source:        "192.168.0.1",
			destination:   "192.168.0.2",
			ipVersion:     "unknown",
			expected:      nil,
			expectedError: ErrFalcoIncompleteL3Flow,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := CreateLayer3Message(tt.source, tt.destination, tt.ipVersion)
			if tt.expectedError != nil {
				require.ErrorIs(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestNewFalcoEventHandler(t *testing.T) {
	eventChan := make(chan string, 1)
	handler := NewFalcoEventHandler(eventChan)

	tests := map[string]struct {
		body           string
		expectedStatus int
		expectedEvent  string
	}{
		"valid event": {
			body:           `{"output": "test event"}`,
			expectedStatus: http.StatusOK,
			expectedEvent:  "test event",
		},
		"invalid JSON": {
			body:           `invalid json`,
			expectedStatus: http.StatusBadRequest,
			expectedEvent:  "",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(t, tt.expectedStatus, rr.Code)

			if tt.expectedStatus == http.StatusOK {
				select {
				case event := <-eventChan:
					assert.Equal(t, tt.expectedEvent, event)
				case <-time.After(100 * time.Millisecond):
					t.Error("Expected event not received")
				}
			}
		})
	}
}
