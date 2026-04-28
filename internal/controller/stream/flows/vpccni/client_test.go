// Copyright 2026 Illumio, Inc. All Rights Reserved.

package vpccni

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/collector"
)

// mockFlowSink mocks the collector.FlowSink interface.
type mockFlowSink struct {
	mock.Mock
}

func (m *mockFlowSink) CacheFlow(ctx context.Context, flow pb.Flow) error {
	args := m.Called(ctx, flow)

	return args.Error(0)
}

func (m *mockFlowSink) IncrementFlowsReceived() {
	m.Called()
}

// VPCCNIClientTestSuite tests the vpccniClient.
type VPCCNIClientTestSuite struct {
	suite.Suite

	logger   *zap.Logger
	client   *vpccniClient
	mockSink *mockFlowSink
}

func TestVPCCNIClientTestSuite(t *testing.T) {
	suite.Run(t, new(VPCCNIClientTestSuite))
}

func (s *VPCCNIClientTestSuite) SetupTest() {
	s.logger = zap.NewNop()
	s.mockSink = &mockFlowSink{}
	s.client = &vpccniClient{
		logger:       s.logger,
		flowSink:     s.mockSink,
		k8sClient:    fake.NewSimpleClientset(),
		pollInterval: 100 * time.Millisecond,
		lastPollTime: make(map[string]time.Time),
	}
}

func (s *VPCCNIClientTestSuite) TestClient_Fields() {
	s.Equal(s.logger, s.client.logger)
	s.Equal(s.mockSink, s.client.flowSink)
	s.Equal(100*time.Millisecond, s.client.pollInterval)
	s.NotNil(s.client.lastPollTime)
}

func (s *VPCCNIClientTestSuite) TestParseLogs_ValidOldFormat() {
	ctx := context.Background()
	logs := `{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"10.0.141.167","Src Port":39197,"Dest IP":"172.20.0.10","Dest Port":53,"Proto":"TCP","Verdict":"ACCEPT"}`

	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	s.client.parseLogs(ctx, logs, "test-pod")

	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 1)
	s.mockSink.AssertNumberOfCalls(s.T(), "IncrementFlowsReceived", 1)
}

func (s *VPCCNIClientTestSuite) TestParseLogs_ValidNewFormat() {
	ctx := context.Background()
	logs := `{"level":"debug","ts":"2026-04-13T21:18:46.888Z","caller":"runtime/asm_amd64.s:1700","msg":"Flow Info: Src IP: 10.0.1.28 Src Port: 55484 Dest IP: 10.0.1.132 Dest Port: 80 Proto TCP Verdict ACCEPT Direction egress"}`

	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	s.client.parseLogs(ctx, logs, "test-pod")

	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 1)
	s.mockSink.AssertNumberOfCalls(s.T(), "IncrementFlowsReceived", 1)
}

func (s *VPCCNIClientTestSuite) TestParseLogs_EmptyLogs() {
	ctx := context.Background()

	s.client.parseLogs(ctx, "", "test-pod")

	s.mockSink.AssertNotCalled(s.T(), "CacheFlow")
	s.mockSink.AssertNotCalled(s.T(), "IncrementFlowsReceived")
}

func (s *VPCCNIClientTestSuite) TestParseLogs_NonFlowLog() {
	ctx := context.Background()
	logs := `{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"some-logger","msg":"Some other message"}`

	s.client.parseLogs(ctx, logs, "test-pod")

	s.mockSink.AssertNotCalled(s.T(), "CacheFlow")
	s.mockSink.AssertNotCalled(s.T(), "IncrementFlowsReceived")
}

func (s *VPCCNIClientTestSuite) TestParseLogs_MultipleLines() {
	ctx := context.Background()
	logs := `{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"10.0.1.1","Src Port":80,"Dest IP":"10.0.1.2","Dest Port":443,"Proto":"TCP","Verdict":"ACCEPT"}
{"level":"info","ts":"2024-09-23T12:36:54.562Z","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"10.0.1.3","Src Port":8080,"Dest IP":"10.0.1.4","Dest Port":53,"Proto":"UDP","Verdict":"ACCEPT"}`

	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	s.client.parseLogs(ctx, logs, "test-pod")

	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 2)
	s.mockSink.AssertNumberOfCalls(s.T(), "IncrementFlowsReceived", 2)
}

func (s *VPCCNIClientTestSuite) TestCleanupStalePods() {
	// Setup: add some pods to lastPollTime
	s.client.lastPollTime["pod-1"] = time.Now()
	s.client.lastPollTime["pod-2"] = time.Now()
	s.client.lastPollTime["pod-3"] = time.Now()

	// Create a pod list with only pod-1 and pod-2
	pods := &corev1.PodList{
		Items: []corev1.Pod{
			{ObjectMeta: metav1.ObjectMeta{Name: "pod-1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "pod-2"}},
		},
	}

	s.client.cleanupStalePods(pods)

	// pod-3 should be removed
	s.Len(s.client.lastPollTime, 2)
	s.Contains(s.client.lastPollTime, "pod-1")
	s.Contains(s.client.lastPollTime, "pod-2")
	s.NotContains(s.client.lastPollTime, "pod-3")
}

func (s *VPCCNIClientTestSuite) TestRun_ContextCancellation() {
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	err := s.client.Run(ctx)

	s.ErrorIs(err, context.Canceled)
}

func (s *VPCCNIClientTestSuite) TestPollAllPods_NoPods() {
	ctx := context.Background()

	err := s.client.pollAllPods(ctx)

	s.NoError(err)
}

func (s *VPCCNIClientTestSuite) TestPollAllPods_WithRunningPod() {
	ctx := context.Background()

	// Create a fake client with an aws-node pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aws-node-xxxxx",
			Namespace: collector.AWSNodeNamespace,
			Labels:    map[string]string{"k8s-app": "aws-node"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: collector.AWSEksNodeagentContainer},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	s.client.k8sClient = fake.NewSimpleClientset(pod)

	// Poll will fail to get logs since fake client doesn't support GetLogs,
	// but the method should still complete without error
	err := s.client.pollAllPods(ctx)

	s.NoError(err)
}
