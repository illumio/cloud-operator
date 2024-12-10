// Copyright 2024 Illumio, Inc. All Rights Reserved.
package controller

import (
	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/stretchr/testify/assert"
)

func (suite *ControllerTestSuite) TestParsePodNetworkInfo() {
	InitRegexFalco()
	tests := map[string]struct {
		input       string
		expected    *pb.FalcoFlow
		expectedErr string
	}{
		"valid input with TCP": {
			input: "srcip=192.168.0.1 dstip=192.168.0.2 srcport=80 dstport=8080 proto=TCP ipversion=IPV4",
			expected: &pb.FalcoFlow{
				Layer3: &pb.IP{
					Source:      "192.168.0.1",
					Destination: "192.168.0.2",
					IpVersion:   pb.IPVersion_IP_VERSION_IPV4,
				},
				Layer4: &pb.Layer4{
					Protocol: &pb.Layer4_Tcp{
						Tcp: &pb.TCP{
							SourcePort:      80,
							DestinationPort: 8080,
						},
					},
				},
			},
			expectedErr: "",
		},
		"valid input with UDP": {
			input: "srcip=10.0.0.1 dstip=10.0.0.2 srcport=443 dstport=8443 proto=UDP ipversion=IPV4",
			expected: &pb.FalcoFlow{
				Layer3: &pb.IP{
					Source:      "10.0.0.1",
					Destination: "10.0.0.2",
					IpVersion:   pb.IPVersion_IP_VERSION_IPV4,
				},
				Layer4: &pb.Layer4{
					Protocol: &pb.Layer4_Udp{
						Udp: &pb.UDP{
							SourcePort:      443,
							DestinationPort: 8443,
						},
					},
				},
			},
			expectedErr: "",
		},
		"missing values for some keys": {
			input: "srcip=192.168.1.1 dstip=192.168.1.2 proto=TCP ipversion=IPV4",
			expected: &pb.FalcoFlow{
				Layer3: &pb.IP{
					Source:      "192.168.1.1",
					Destination: "192.168.1.2",
					IpVersion:   pb.IPVersion_IP_VERSION_IPV4,
				},
				Layer4: &pb.Layer4{
					Protocol: &pb.Layer4_Tcp{
						Tcp: &pb.TCP{},
					},
				},
			},
			expectedErr: "",
		},
		"invalid input format": {
			input:       "blah=invalid evan",
			expected:    &pb.FalcoFlow{},
			expectedErr: "ignoring falco event, not a network flow",
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			result, err := parsePodNetworkInfo(tt.input)
			if tt.expectedErr != "" {
				assert.EqualError(suite.T(), err, tt.expectedErr)
			} else {
				assert.NoError(suite.T(), err)
				assert.Equal(suite.T(), tt.expected, result)
			}
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
		proto          string
		srcPort        uint32
		dstPort        uint32
		ipVersion      string
		expected       *pb.Layer4
		expectedErrMsg string
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
			expectedErrMsg: "",
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
			expectedErrMsg: "",
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
			expectedErrMsg: "",
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
			expectedErrMsg: "",
		},
		"Unknown protocol": {
			proto:          "unknown",
			srcPort:        0,
			dstPort:        0,
			ipVersion:      "",
			expected:       &pb.Layer4{},
			expectedErrMsg: "",
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			result, err := CreateLayer4Message(tt.proto, tt.srcPort, tt.dstPort, tt.ipVersion)
			if tt.expectedErrMsg != "" {
				assert.Error(suite.T(), err)
				assert.EqualError(suite.T(), err, tt.expectedErrMsg)
			} else {
				assert.NoError(suite.T(), err)
				assert.Equal(suite.T(), tt.expected, result)
			}
		})
	}
}

func (suite *ControllerTestSuite) TestCreateLayer3Message() {
	tests := map[string]struct {
		source      string
		destination string
		ipVersion   string
		expected    *pb.IP
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
		},
		"Unspecified IP version": {
			source:      "192.168.0.1",
			destination: "192.168.0.2",
			ipVersion:   "unknown",
			expected: &pb.IP{
				Source:      "192.168.0.1",
				Destination: "192.168.0.2",
				IpVersion:   pb.IPVersion_IP_VERSION_IP_NOT_USED_UNSPECIFIED,
			},
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			result, err := createLayer3Message(tt.source, tt.destination, tt.ipVersion)
			assert.NoError(suite.T(), err)
			assert.Equal(suite.T(), tt.expected, result)
		})
	}
}
