// Copyright 2026 Illumio, Inc. All Rights Reserved.

package collector

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestIsCalicoEnterpriseDeployed(t *testing.T) {
	tests := map[string]struct {
		namespaceExists bool
		expectedResult  bool
	}{
		"Calico Enterprise namespace exists": {
			namespaceExists: true,
			expectedResult:  true,
		},
		"Calico Enterprise namespace does not exist": {
			namespaceExists: false,
			expectedResult:  false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clientset := fake.NewClientset()
			if tt.namespaceExists {
				_, err := clientset.CoreV1().Namespaces().Create(context.TODO(), &v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "calico-system",
					},
				}, metav1.CreateOptions{})
				require.NoError(t, err)
			}

			logger := zap.NewNop()
			result := IsCalicoEnterpriseDeployed(context.TODO(), logger, "calico-system", clientset)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestParseSyslogFlow(t *testing.T) {
	// Fixtures are real captured Felix syslog records (key="value" body,
	// alphabetically ordered, numeric proto, mixed quoting on counters).
	tests := map[string]struct {
		line     string
		expected CalicoFlow
		err      error
	}{
		"TCP allow flow": {
			line: `<14>2026-06-29T22:45:59Z minikube calico-felix[51097]: INFO <nil> <nil>:  action="allow" dstIP="10.244.120.74" dstPort="8080" inBytes=4454 inPackets=12 outBytes=1200 outPackets=10 policy="intrusion-detection-controller" proto="6" srcIP="10.244.120.78" srcPort="48710" trafficDir="outbound" updateType=report`,
			expected: CalicoFlow{
				SourceIP:        "10.244.120.78",
				DestinationIP:   "10.244.120.74",
				SourcePort:      48710,
				DestinationPort: 8080,
				Protocol:        TCP,
				IPVersion:       IPv4,
				StartTimestamp:  mustTimestamp(t, "2026-06-29T22:45:59Z"),
			},
		},
		"UDP deny flow": {
			line: `<14>2026-06-29T22:45:59Z minikube calico-felix[51097]: INFO <nil> <nil>:  action="deny" dstIP="10.96.0.10" dstPort="53" proto="17" srcIP="10.244.120.78" srcPort="33001" trafficDir="outbound" updateType=report`,
			expected: CalicoFlow{
				SourceIP:        "10.244.120.78",
				DestinationIP:   "10.96.0.10",
				SourcePort:      33001,
				DestinationPort: 53,
				Protocol:        UDP,
				IPVersion:       IPv4,
				StartTimestamp:  mustTimestamp(t, "2026-06-29T22:45:59Z"),
			},
		},
		"IPv6 TCP flow infers ipv6": {
			line: `<14>2026-06-29T22:45:59Z minikube calico-felix[51097]: INFO <nil> <nil>:  action="allow" dstIP="fd00::2" dstPort="443" proto="6" srcIP="fd00::1" srcPort="51000" updateType=report`,
			expected: CalicoFlow{
				SourceIP:        "fd00::1",
				DestinationIP:   "fd00::2",
				SourcePort:      51000,
				DestinationPort: 443,
				Protocol:        TCP,
				IPVersion:       IPv6,
				StartTimestamp:  mustTimestamp(t, "2026-06-29T22:45:59Z"),
			},
		},
		"missing five-tuple field is incomplete": {
			line: `<14>2026-06-29T22:45:59Z minikube calico-felix[51097]: INFO <nil> <nil>:  action="allow" dstIP="10.244.120.74" dstPort="8080" proto="6" updateType=report`,
			err:  ErrCalicoIncompleteFlow,
		},
		"unknown protocol is rejected": {
			line: `<14>2026-06-29T22:45:59Z minikube calico-felix[51097]: INFO <nil> <nil>:  action="allow" dstIP="10.244.120.74" dstPort="8080" proto="99" srcIP="10.244.120.78" srcPort="48710" updateType=report`,
			err:  ErrCalicoUnknownProto,
		},
		"non-numeric port is rejected": {
			line: `<14>2026-06-29T22:45:59Z minikube calico-felix[51097]: INFO <nil> <nil>:  action="allow" dstIP="10.244.120.74" dstPort="notaport" proto="6" srcIP="10.244.120.78" srcPort="48710" updateType=report`,
			err:  ErrCalicoInvalidPort,
		},
		"non-flow line is incomplete": {
			line: `<14>2026-06-29T22:45:59Z minikube calico-felix[51097]: INFO Starting Felix`,
			err:  ErrCalicoIncompleteFlow,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			flow, err := ParseSyslogFlow(tt.line)
			if tt.err != nil {
				assert.ErrorIs(t, err, tt.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected.SourceIP, flow.SourceIP)
			assert.Equal(t, tt.expected.DestinationIP, flow.DestinationIP)
			assert.Equal(t, tt.expected.SourcePort, flow.SourcePort)
			assert.Equal(t, tt.expected.DestinationPort, flow.DestinationPort)
			assert.Equal(t, tt.expected.Protocol, flow.Protocol)
			assert.Equal(t, tt.expected.IPVersion, flow.IPVersion)
			assert.True(t, tt.expected.StartTimestamp.AsTime().Equal(flow.StartTimestamp.AsTime()))
		})
	}
}

func mustTimestamp(t *testing.T, value string) *timestamppb.Timestamp {
	t.Helper()

	parsed, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)

	return timestamppb.New(parsed)
}
