package controller

import (
	"context"
	"testing"

	netflows "github.com/netsampler/goflow2/decoders/netflow"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (suite *ControllerTestSuite) TestIsOVNDeployed() {
	tests := map[string]struct {
		namespaceExists bool
		expectedResult  bool
	}{
		"OVN namespace exists": {
			namespaceExists: false,
			expectedResult:  false,
		},
		"OVN namespace does not exist": {
			namespaceExists: true,
			expectedResult:  true,
		},
	}

	for name, tt := range tests {
		suite.Run(name, func() {
			if tt.namespaceExists {
				_, err := suite.clientset.CoreV1().Namespaces().Create(context.TODO(), &v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "openshift-ovn-kubernetes",
					},
				}, metav1.CreateOptions{})
				assert.NoError(suite.T(), err)
			}

			logger := zap.NewNop()
			sm := &streamManager{}
			result := sm.isOVNDeployed(logger)
			assert.Equal(suite.T(), tt.expectedResult, result)
		})
	}
}

func TestConvertProtocol(t *testing.T) {
	tests := map[string]struct {
		input    []byte
		expected string
	}{
		"ICMP protocol": {
			input:    []byte{1},
			expected: "icmpt",
		},
		"TCP protocol": {
			input:    []byte{6},
			expected: "tcp",
		},
		"UDP protocol": {
			input:    []byte{17},
			expected: "udp",
		},
		"SCTP protocol": {
			input:    []byte{132},
			expected: "sctp",
		},
		"Unknown protocol": {
			input:    []byte{255},
			expected: "Unknown",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := convertProtocol(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertIpVersion(t *testing.T) {
	tests := map[string]struct {
		input    []byte
		expected string
	}{
		"IPv4 version": {
			input:    []byte{4},
			expected: "ipv4",
		},
		"IPv6 version": {
			input:    []byte{6},
			expected: "ipv6",
		},
		"Unknown version": {
			input:    []byte{0},
			expected: "Unknown",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := convertIpVersion(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseIPv4Address(t *testing.T) {
	tests := map[string]struct {
		input    []byte
		expected string
	}{
		"Valid IPv4 address": {
			input:    []byte{192, 168, 1, 1},
			expected: "192.168.1.1",
		},
		"Another valid IPv4 address": {
			input:    []byte{10, 0, 0, 1},
			expected: "10.0.0.1",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := parseIPv4Address(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParsePort(t *testing.T) {
	tests := map[string]struct {
		input    []byte
		expected uint16
	}{
		"Valid port 80": {
			input:    []byte{0, 80},
			expected: 80,
		},
		"Valid port 8080": {
			input:    []byte{31, 144},
			expected: 8080,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := parsePort(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestProcessDataRecord(t *testing.T) {
	tests := map[string]struct {
		input    netflows.DataRecord
		expected OVNFlow
	}{
		"Valid data record": {
			input: netflows.DataRecord{
				Values: []netflows.DataField{
					{Type: 8, Value: []byte{192, 168, 1, 1}},  // Source IP
					{Type: 12, Value: []byte{192, 168, 1, 2}}, // Destination IP
					{Type: 7, Value: []byte{0, 80}},           // Source Port
					{Type: 11, Value: []byte{31, 144}},        // Destination Port
					{Type: 4, Value: []byte{6}},               // Protocol (TCP)
					{Type: 60, Value: []byte{4}},              // IP Version (IPv4)
				},
			},
			expected: OVNFlow{
				SourceIP:        "192.168.1.1",
				DestinationIP:   "192.168.1.2",
				SourcePort:      80,
				DestinationPort: 8080,
				Protocol:        "tcp",
				IPVersion:       "ipv4",
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := processDataRecord(tt.input)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
