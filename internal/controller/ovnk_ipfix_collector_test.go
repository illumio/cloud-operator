// Copyright 2025 Illumio, Inc. All Rights Reserved.

package controller

import (
	"context"
	"testing"
	"time"

	netflows "github.com/netsampler/goflow2/decoders/netflow"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (suite *ControllerTestSuite) TestIsOVNKDeployed() {
	tests := map[string]struct {
		namespaceExists bool
		expectedResult  bool
	}{
		"OVNK namespace exists": {
			namespaceExists: false,
			expectedResult:  false,
		},
		"OVNK namespace does not exist": {
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
			} else {
				// Add polling logic to ensure namespace deletion
				for {
					err := suite.clientset.CoreV1().Namespaces().Delete(context.TODO(), "openshift-ovn-kubernetes", metav1.DeleteOptions{})
					if errors.IsNotFound(err) {
						break // Namespace is deleted
					}
					if err != nil {
						suite.T().Fatal("Error while checking namespace deletion: " + err.Error())
					}
					time.Sleep(100 * time.Millisecond) // Wait before retrying
				}
			}

			logger := zap.NewNop()
			sm := &streamManager{}
			result := sm.isOVNKDeployed(context.TODO(), logger, "openshift-ovn-kubernetes", suite.clientset)
			assert.Equal(suite.T(), tt.expectedResult, result)
		})
	}
}

func TestConvertProtocol(t *testing.T) {
	tests := map[string]struct {
		input    []byte
		expected string
		err      bool
	}{
		"ICMP protocol": {
			input:    []byte{1},
			expected: "icmpt",
			err:      false,
		},
		"TCP protocol": {
			input:    []byte{6},
			expected: "tcp",
			err:      false,
		},
		"UDP protocol": {
			input:    []byte{17},
			expected: "udp",
			err:      false,
		},
		"SCTP protocol": {
			input:    []byte{132},
			expected: "sctp",
			err:      false,
		},
		"Unknown protocol": {
			input:    []byte{255},
			expected: "",
			err:      true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := parseProtocol(tt.input)
			if tt.err {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestParseIPv4Address(t *testing.T) {
	tests := map[string]struct {
		input    []byte
		expected string
		err      bool
	}{
		"Valid IPv4 address": {
			input:    []byte{192, 168, 1, 1},
			expected: "192.168.1.1",
			err:      false,
		},
		"Invalid IPv4 address": {
			input:    []byte{192, 168},
			expected: "",
			err:      true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := parseIPv4Address(tt.input)
			if tt.err {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestParseIPv6Address(t *testing.T) {
	tests := map[string]struct {
		input    []byte
		expected string
		err      bool
	}{
		"Valid IPv6 address": {
			input:    []byte{0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
			expected: "2001:db8::1",
			err:      false,
		},
		"Invalid IPv6 address": {
			input:    []byte{0x20, 0x01, 0x0d, 0xb8},
			expected: "",
			err:      true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := parseIPv6Address(tt.input)
			if tt.err {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestParsePort(t *testing.T) {
	tests := map[string]struct {
		input    []byte
		expected uint16
		err      bool
	}{
		"Valid port 80": {
			input:    []byte{0, 80},
			expected: 80,
			err:      false,
		},
		"Invalid port": {
			input:    []byte{80},
			expected: 0,
			err:      true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := parsePort(tt.input)
			if tt.err {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestParseProtocol(t *testing.T) {
	tests := map[string]struct {
		input    []byte
		expected string
		err      bool
	}{
		"TCP protocol": {
			input:    []byte{6},
			expected: "tcp",
			err:      false,
		},
		"Unknown protocol": {
			input:    []byte{255},
			expected: "",
			err:      true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := parseProtocol(tt.input)
			if tt.err {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestParseIPVersion(t *testing.T) {
	tests := map[string]struct {
		input    []byte
		expected string
		err      bool
	}{
		"IPv4 version": {
			input:    []byte{4},
			expected: "ipv4",
			err:      false,
		},
		"Unknown version": {
			input:    []byte{0},
			expected: "",
			err:      true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := parseIPVersion(tt.input)
			if tt.err {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestProcessDataRecord(t *testing.T) {
	tests := map[string]struct {
		input    netflows.DataRecord
		expected OVNKFlow
		err      bool
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
			expected: OVNKFlow{
				SourceIP:        "192.168.1.1",
				DestinationIP:   "192.168.1.2",
				SourcePort:      80,
				DestinationPort: 8080,
				Protocol:        "tcp",
				IPVersion:       "ipv4",
			},
			err: false,
		},
		"Invalid data record": {
			input: netflows.DataRecord{
				Values: []netflows.DataField{
					{Type: 8, Value: []byte{192, 168}}, // Invalid Source IP
				},
			},
			expected: OVNKFlow{},
			err:      true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := processDataRecord(tt.input, 0)
			if tt.err {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}
