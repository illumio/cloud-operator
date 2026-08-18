// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsautomode

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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

// scriptedFetcher returns a queued sequence of responses per node, so tests can
// simulate a poll sequence (append, truncation, rotation, restart, errors).
//
// list/open default to "no rotations present": list returns an empty slice and
// open returns errRotatedFileNotFound, so existing active-file tests exercise the
// normal (no-rotation) path unchanged. Rotation-aware tests set rotationsByNode /
// rotatedData explicitly.
type scriptedFetcher struct {
	mu        sync.Mutex
	responses map[string][]fetchResult
	calls     map[string]int

	// rotationsByNode is the directory listing returned by list() per node. When a
	// test needs the listing to change across polls, it can queue slices here and
	// they are consumed one per list() call (last one repeats).
	rotationsByNode map[string][][]RotatedFile
	listCalls       map[string]int

	// rotatedData maps "node|filename" to the raw bytes open() streams for that
	// rotated file. Missing entries make open() return errRotatedFileNotFound.
	rotatedData map[string][]byte
	openCalls   map[string]int
}

type fetchResult struct {
	data []byte
	err  error
}

func newScriptedFetcher() *scriptedFetcher {
	return &scriptedFetcher{
		responses:       make(map[string][]fetchResult),
		calls:           make(map[string]int),
		rotationsByNode: make(map[string][][]RotatedFile),
		listCalls:       make(map[string]int),
		rotatedData:     make(map[string][]byte),
		openCalls:       make(map[string]int),
	}
}

func (f *scriptedFetcher) queue(node string, data []byte, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.responses[node] = append(f.responses[node], fetchResult{data: data, err: err})
}

// queueRotations appends one directory listing to be returned by the next list()
// call for a node. Successive calls consume successive listings (last repeats).
//
//nolint:unparam // node is kept for symmetry with queue/setRotatedData and multi-node tests
func (f *scriptedFetcher) queueRotations(node string, files []RotatedFile) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.rotationsByNode[node] = append(f.rotationsByNode[node], files)
}

// setRotatedData registers the bytes open() streams for a node's rotated file.
//
//nolint:unparam // node is kept for symmetry with queue/queueRotations and multi-node tests
func (f *scriptedFetcher) setRotatedData(node, filename string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.rotatedData[node+"|"+filename] = data
}

// fetchActive simulates a Range-capable node-proxy server over the queued active
// file for a node. Each call consumes one queued entry (the "current" active file),
// repeating the last entry once the script is exhausted, mirroring a file that stops
// changing. Given that entry's bytes it answers like a real server:
//
//   - rangeStart <= 0            -> 200, whole file from offset 0.
//   - 0 < rangeStart < len       -> 206, tail from rangeStart (bodyStart=rangeStart).
//   - rangeStart >= len          -> 416, no body (range past EOF: shrink/rotation).
//
// A reset (streamActive needReset) makes the client re-fetch from 0 within the same
// poll, consuming a second entry; because the script repeats its last entry, a reset
// that is the final scripted poll re-reads identical bytes. Tests that queue further
// polls AFTER a reset should account for that extra consumption.
func (f *scriptedFetcher) fetchActive(_ context.Context, nodeName string, rangeStart int64) (*activeStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	idx := f.calls[nodeName]
	f.calls[nodeName]++

	resps := f.responses[nodeName]

	var res fetchResult

	switch {
	case len(resps) == 0:
		res = fetchResult{}
	case idx >= len(resps):
		res = resps[len(resps)-1]
	default:
		res = resps[idx]
	}

	if res.err != nil {
		return nil, res.err
	}

	data := res.data
	n := int64(len(data))

	switch {
	case rangeStart <= 0:
		return &activeStream{statusCode: http.StatusOK, bodyStart: 0, Body: io.NopCloser(bytes.NewReader(data))}, nil
	case rangeStart >= n:
		return &activeStream{statusCode: http.StatusRequestedRangeNotSatisfiable}, nil
	default:
		return &activeStream{statusCode: http.StatusPartialContent, bodyStart: rangeStart, Body: io.NopCloser(bytes.NewReader(data[rangeStart:]))}, nil
	}
}

func (f *scriptedFetcher) list(_ context.Context, nodeName string) ([]RotatedFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	listings := f.rotationsByNode[nodeName]
	if len(listings) == 0 {
		return nil, nil
	}

	idx := f.listCalls[nodeName]
	f.listCalls[nodeName]++

	if idx >= len(listings) {
		idx = len(listings) - 1
	}

	return listings[idx], nil
}

func (f *scriptedFetcher) open(_ context.Context, nodeName, filename string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.openCalls[nodeName+"|"+filename]++

	data, ok := f.rotatedData[nodeName+"|"+filename]
	if !ok {
		return nil, errRotatedFileNotFound
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

// recentTS is a timestamp within the stream time window, so flows built with it
// are not dropped by the MaxFlowAge stale-flow filter in collector.CacheFlowLine.
// It is fixed once at package init to a value close to test-run time.
var recentTS = time.Now().Add(-time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")

// Flow log fixtures use recentTS so the collector's stale-flow filter keeps them.
var (
	oldFmtFlow1 = `{"level":"info","ts":"` + recentTS + `","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"10.0.1.1","Src Port":80,"Dest IP":"10.0.1.2","Dest Port":443,"Proto":"TCP","Verdict":"ACCEPT"}`
	oldFmtFlow2 = `{"level":"info","ts":"` + recentTS + `","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"10.0.1.3","Src Port":8080,"Dest IP":"10.0.1.4","Dest Port":53,"Proto":"UDP","Verdict":"ACCEPT"}`
	oldFmtFlow3 = `{"level":"info","ts":"` + recentTS + `","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"10.0.1.5","Src Port":1234,"Dest IP":"10.0.1.6","Dest Port":80,"Proto":"TCP","Verdict":"ACCEPT"}`
)

type AutoModeClientTestSuite struct {
	suite.Suite

	logger   *zap.Logger
	mockSink *mockFlowSink
	fetcher  *scriptedFetcher
}

func TestAutoModeClientTestSuite(t *testing.T) {
	suite.Run(t, new(AutoModeClientTestSuite))
}

func (s *AutoModeClientTestSuite) SetupTest() {
	s.logger = zap.NewNop()
	s.mockSink = &mockFlowSink{}
	s.fetcher = newScriptedFetcher()
}

func (s *AutoModeClientTestSuite) newClient(nodes ...*corev1.Node) *autoModeClient {
	objs := make([]runtime.Object, 0, len(nodes))
	for _, n := range nodes {
		objs = append(objs, n)
	}

	return &autoModeClient{
		logger:             s.logger,
		flowSink:           s.mockSink,
		k8sClient:          fake.NewSimpleClientset(objs...),
		fetcher:            s.fetcher,
		pollInterval:       10 * time.Millisecond,
		maxConcurrentPolls: 2,
		checkpoints:        newCheckpointStore(),
	}
}

func autoModeNode(name, uid string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			UID:    types.UID(uid),
			Labels: map[string]string{collector.EKSComputeTypeLabel: collector.EKSComputeTypeAuto},
		},
	}
}

func (s *AutoModeClientTestSuite) TestPollNode_ParsesFlows() {
	ctx := context.Background()

	s.fetcher.queue("node-a", []byte(oldFmtFlow1+"\n"+oldFmtFlow2+"\n"), nil)

	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	c := s.newClient()
	err := c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger)

	s.Require().NoError(err)
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 2)
	s.mockSink.AssertNumberOfCalls(s.T(), "IncrementFlowsReceived", 2)
}

func (s *AutoModeClientTestSuite) TestPollNode_DropsStaleFlows() {
	ctx := context.Background()

	// A flow well outside the stream time window must be parsed but not cached.
	staleFlow := `{"level":"info","ts":"2024-09-23T12:36:53.562Z","logger":"ebpf-client","msg":"Flow Info: ","Src IP":"10.0.1.1","Src Port":80,"Dest IP":"10.0.1.2","Dest Port":443,"Proto":"TCP","Verdict":"ACCEPT"}`
	s.fetcher.queue("node-a", []byte(staleFlow+"\n"), nil)

	c := s.newClient()
	err := c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger)

	s.Require().NoError(err)
	s.mockSink.AssertNotCalled(s.T(), "CacheFlow")
	s.mockSink.AssertNotCalled(s.T(), "IncrementFlowsReceived")
	// The checkpoint still advances past the stale (but valid) line.
	s.NotZero(c.checkpoints.get("node-a").ByteOffset)
}

func (s *AutoModeClientTestSuite) TestPollNode_IncrementalAppendEmitsOnlyNew() {
	ctx := context.Background()
	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	c := s.newClient()

	// First poll: two flows.
	s.fetcher.queue("node-a", []byte(oldFmtFlow1+"\n"+oldFmtFlow2+"\n"), nil)
	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 2)

	// Second poll: same two plus a third; only the third is new.
	s.fetcher.queue("node-a", []byte(oldFmtFlow1+"\n"+oldFmtFlow2+"\n"+oldFmtFlow3+"\n"), nil)
	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 3)
}

// TestPollNode_LargeFileRangeContinuation drives the steady-state OOM-fix path
// end-to-end: an active file larger than validationOverlap so the second poll issues
// a real Range request (HTTP 206 from the scripted server) instead of reading the
// whole file. It asserts the checkpoint's boundary record is re-validated and only
// the newly-appended flows are emitted — the prior >1 MiB prefix is not re-sent.
func (s *AutoModeClientTestSuite) TestPollNode_LargeFileRangeContinuation() {
	ctx := context.Background()
	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	c := s.newClient()

	// Build a prefix strictly larger than validationOverlap so the next poll's
	// rangeStart (ByteOffset - validationOverlap) is > 0 and a 206 is served.
	line := oldFmtFlow1 + "\n"
	nPrefix := validationOverlap/len(line) + 100

	var prefix bytes.Buffer
	for range nPrefix {
		prefix.WriteString(line)
	}

	s.Require().Greater(prefix.Len(), validationOverlap)

	// First poll: whole large file streamed from 0 (bootstrap). Every line cached.
	s.fetcher.queue("node-a", prefix.Bytes(), nil)
	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", nPrefix)

	offset := c.checkpoints.get("node-a").ByteOffset
	s.Equal(prefix.Len(), offset)
	s.Greater(offset, validationOverlap) // guarantees the next poll uses a Range

	// Second poll: same prefix plus two new flows. The Range request re-reads only
	// the overlap tail; the boundary record validates; only the two new flows emit.
	appended := prefix.String() + oldFmtFlow2 + "\n" + oldFmtFlow3 + "\n"
	s.fetcher.queue("node-a", []byte(appended), nil)
	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", nPrefix+2)

	s.Equal(len(appended), c.checkpoints.get("node-a").ByteOffset)
}

func (s *AutoModeClientTestSuite) TestPollNode_FetchErrorDoesNotAdvanceOffset() {
	ctx := context.Background()
	c := s.newClient()

	s.fetcher.queue("node-a", nil, errors.New("403 forbidden"))
	err := c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger)

	s.Require().Error(err)
	// Offset stays at zero; no flows cached.
	s.Equal(0, c.checkpoints.get("node-a").ByteOffset)
	s.mockSink.AssertNotCalled(s.T(), "CacheFlow")
}

func (s *AutoModeClientTestSuite) TestPollNode_TruncationReprocesses() {
	ctx := context.Background()
	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	c := s.newClient()

	s.fetcher.queue("node-a", []byte(oldFmtFlow1+"\n"+oldFmtFlow2+"\n"), nil)
	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 2)

	// File truncated to a single (different) flow.
	s.fetcher.queue("node-a", []byte(oldFmtFlow3+"\n"), nil)
	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 3)
}

func (s *AutoModeClientTestSuite) TestPollAllNodes_PollsEachAutoModeNode() {
	ctx := context.Background()
	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	s.fetcher.queue("node-a", []byte(oldFmtFlow1+"\n"), nil)
	s.fetcher.queue("node-b", []byte(oldFmtFlow2+"\n"), nil)

	c := s.newClient(autoModeNode("node-a", "uid-a"), autoModeNode("node-b", "uid-b"))
	c.pollAllNodes(ctx)

	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 2)
}

func (s *AutoModeClientTestSuite) TestPollAllNodes_DropsStaleCheckpoints() {
	ctx := context.Background()
	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	s.fetcher.queue("node-a", []byte(oldFmtFlow1+"\n"), nil)

	c := s.newClient(autoModeNode("node-a", "uid-a"))
	// Pre-seed a checkpoint for a node that no longer exists.
	c.checkpoints.set("gone", &nodeLogCheckpoint{NodeName: "gone", ByteOffset: 10})

	c.pollAllNodes(ctx)

	// "gone" removed; "node-a" retained.
	s.Equal(0, c.checkpoints.get("gone").ByteOffset) // recreated zero-valued
	s.NotZero(c.checkpoints.get("node-a").ByteOffset)
}

func (s *AutoModeClientTestSuite) TestRun_ContextCancellation() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := s.newClient()
	err := c.Run(ctx)

	s.ErrorIs(err, context.Canceled)
}
