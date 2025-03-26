// Copyright 2024 Illumio, Inc. All Rights Reserved.

package controller

import (
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (suite *ControllerTestSuite) TestParsePodNetworkInfo() {
	//t := "1987-02-22T00:39:07.267635635+0000"
	ts, _ := convertStringToTimestamp("1987-02-22T00:39:07.267635635+0000")

	tests := map[string]struct {
		input    string
		expected *pb.FalcoFlow
		err      error
	}{
		"valid TCP flow": {
			input: "time=1987-02-22T00:39:07.267635635+0000\t srcip=192.168.1.1 dstip=192.168.1.2 srcport=1234 dstport=5678 proto=tcp ipversion=ipv4",
			expected: &pb.FalcoFlow{
				TimeStamp: ts,
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
			expected: &pb.FalcoFlow{
				TimeStamp: ts,
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
		suite.Run(name, func() {
			result, err := parsePodNetworkInfo(tt.input)
			if tt.err != nil {
				assert.NotNil(suite.T(), err)
				assert.Equal(suite.T(), tt.err.Error(), err.Error())
			} else {
				assert.Nil(suite.T(), err)
			}
			assert.Equal(suite.T(), tt.expected, result)
		})
	}
}

// TestFilterIllumioTraffic tests the filterIllumioTraffic function
func (suite *ControllerTestSuite) TestFilterIllumioTraffic() {
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
		suite.Run(name, func() {
			result := filterIllumioTraffic(tt.input)
			assert.Equal(suite.T(), tt.expected, result)
		})
	}
}

func (suite *ControllerTestSuite) TestCreateLayer4Message() {
	tests := map[string]struct {
		proto       string
		srcPort     uint32
		dstPort     uint32
		ipVersion   string
		expected    *pb.Layer4
		expectedErr error
	}{
		"TCP protocol": {
			proto:     "tcp",
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
			proto:     "udp",
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
			proto:     "icmp",
			srcPort:   0,
			dstPort:   0,
			ipVersion: "ipv4",
			expected: &pb.Layer4{
				Protocol: &pb.Layer4_Icmpv4{
					Icmpv4: &pb.ICMPv4{},
				},
			},
			expectedErr: nil,
		},
		"ICMP protocol with IPv6": {
			proto:     "icmp",
			srcPort:   0,
			dstPort:   0,
			ipVersion: "ipv6",
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
		suite.Run(name, func() {
			result, err := createLayer4Message(tt.proto, tt.srcPort, tt.dstPort, tt.ipVersion)
			if err != nil {
				assert.NotNil(suite.T(), err)
				assert.Equal(suite.T(), tt.expectedErr.Error(), err.Error())
			} else {
				assert.Nil(suite.T(), err)
			}
			assert.Equal(suite.T(), tt.expected, result)
		})
	}
}

func (suite *ControllerTestSuite) TestCreateLayer3Message() {
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
			ipVersion:   "ipv4",
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
			ipVersion:   "ipv6",
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
		suite.Run(name, func() {
			result, err := createLayer3Message(tt.source, tt.destination, tt.ipVersion)
			if err != nil {
				assert.NotNil(suite.T(), err)
				assert.Equal(suite.T(), tt.expectedError.Error(), err.Error())
			} else {
				assert.Nil(suite.T(), err)
			}
			assert.Equal(suite.T(), tt.expected, result)
		})
	}
}

func (suite *ControllerTestSuite) TestRemoveTrailingTab() {
	tests := map[string]struct {
		input    string
		expected string
	}{
		"No trailing tab": {
			input:    "1987-02-22T00:39:07.267635635+0000",
			expected: "1987-02-22T00:39:07.267635635+0000",
		},
		"One trailing tab": {
			input:    "1987-02-22T00:39:07.267635635+0000\t",
			expected: "1987-02-22T00:39:07.267635635+0000",
		},
		"Multiple trailing tabs": {
			input:    "1987-02-22T00:39:07.267635635+0000\t\t",
			expected: "1987-02-22T00:39:07.267635635+0000",
		},
		"Tabs in different parts": {
			input:    "\t\t1987-02-22T00:39:07.267635635+0000\t\t",
			expected: "\t\t1987-02-22T00:39:07.267635635+0000",
		},
		"Leading and trailing tab": {
			input:    "\t1987-02-22T00:39:07.267635635+0000\t",
			expected: "\t1987-02-22T00:39:07.267635635+0000",
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			result := removeTrailingTab(tt.input)
			assert.Equal(suite.T(), tt.expected, result)
		})
	}
}

func (suite *ControllerTestSuite) TestConvertStringToTimestamp() {
	tests := map[string]struct {
		input    string
		expected *timestamppb.Timestamp
	}{
		"Valid ISO 8601 time string": {
			input: "1987-02-22T00:39:07.267635635+0000",
			expected: &timestamppb.Timestamp{
				Seconds: 540952747,
				Nanos:   267635635,
			},
		},
		"Invalid ISO 8601 time string": {
			input:    "2022-10-15T15:04:05", // Missing time zone
			expected: nil,                   // Expected error
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			result, err := convertStringToTimestamp(tt.input)
			if tt.expected == nil {
				assert.NotNil(suite.T(), err, "Expected an error but got nil")
			} else {
				assert.Nil(suite.T(), err, "Unexpected error occurred")
				assert.Equal(suite.T(), tt.expected, result)
			}
		})
	}

}
