// Copyright 2025 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"testing"

	netflows "github.com/netsampler/goflow2/decoders/netflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
)

func TestIsOVNKDeployed(t *testing.T) {
	tests := map[string]struct {
		namespaceExists bool
		expectedResult  bool
	}{
		"OVNK namespace exists": {
			namespaceExists: true,
			expectedResult:  true,
		},
		"OVNK namespace does not exist": {
			namespaceExists: false,
			expectedResult:  false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset()
			if tt.namespaceExists {
				_, err := clientset.CoreV1().Namespaces().Create(context.TODO(), &v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "openshift-ovn-kubernetes",
					},
				}, metav1.CreateOptions{})
				require.NoError(t, err)
			}

			logger := zap.NewNop()
			result := IsOVNKDeployed(context.TODO(), logger, "openshift-ovn-kubernetes", clientset)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestParseProtocol(t *testing.T) {
	tests := map[string]struct {
		input    []byte
		expected string
		err      bool
	}{
		"ICMP protocol": {
			input:    []byte{1},
			expected: ICMP,
			err:      false,
		},
		"TCP protocol": {
			input:    []byte{6},
			expected: TCP,
			err:      false,
		},
		"UDP protocol": {
			input:    []byte{17},
			expected: UDP,
			err:      false,
		},
		"SCTP protocol": {
			input:    []byte{132},
			expected: SCTP,
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
			result, err := ParseProtocol(tt.input)
			if tt.err {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
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
			result, err := ParseIPv4Address(tt.input)
			if tt.err {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
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
			result, err := ParseIPv6Address(tt.input)
			if tt.err {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
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
			result, err := ParsePort(tt.input)
			if tt.err {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
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
			expected: IPv4,
			err:      false,
		},
		"IPv6 version": {
			input:    []byte{6},
			expected: IPv6,
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
			result, err := ParseIPVersion(tt.input)
			if tt.err {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
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
					{Type: 11, Value: []byte{31, 144}},        // Destination Port (8080)
					{Type: 4, Value: []byte{6}},               // Protocol (TCP)
					{Type: 60, Value: []byte{4}},              // IP Version (IPv4)
				},
			},
			expected: OVNKFlow{
				SourceIP:        "192.168.1.1",
				DestinationIP:   "192.168.1.2",
				SourcePort:      80,
				DestinationPort: 8080,
				Protocol:        TCP,
				IPVersion:       IPv4,
			},
			err: false,
		},
		"Invalid data record - bad source IP": {
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
			result, err := ProcessDataRecord(tt.input, 0)
			if tt.err {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestNewOVNKCollector(t *testing.T) {
	logger := zap.NewNop()
	flowSink := &mockFlowSink{}

	collector := NewOVNKCollector(logger, "4739", flowSink)

	assert.NotNil(t, collector)
	assert.Equal(t, "4739", collector.ipfixCollectorPort)
	assert.NotNil(t, collector.flowSink)
}

// mockFlowSink is a simple mock implementation of FlowSink for testing.
type mockFlowSink struct {
	flowsCached   int
	flowsReceived int
}

func (m *mockFlowSink) CacheFlow(_ context.Context, _ pb.Flow) error {
	m.flowsCached++

	return nil
}

func (m *mockFlowSink) IncrementFlowsReceived() {
	m.flowsReceived++
}
