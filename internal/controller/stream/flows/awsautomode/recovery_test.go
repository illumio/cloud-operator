// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsautomode

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"strings"

	"github.com/stretchr/testify/mock"
	"k8s.io/apimachinery/pkg/types"
)

// gzipBytes compresses s for use as rotated ".gz" file content in tests.
func gzipBytes(s string) []byte {
	var buf bytes.Buffer

	w := gzip.NewWriter(&buf)
	_, _ = w.Write([]byte(s))
	_ = w.Close()

	return buf.Bytes()
}

func flowLines(flows ...string) string {
	var out strings.Builder
	for _, f := range flows {
		out.WriteString(f)
		out.WriteString("\n")
	}

	return out.String()
}

// TestBootstrap_NoRotations: brand-new node, empty dir listing -> read active file
// from zero, LastRotationID stays "".
func (s *AutoModeClientTestSuite) TestBootstrap_NoRotations() {
	ctx := context.Background()
	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	c := s.newClient()
	s.fetcher.queue("node-a", []byte(flowLines(oldFmtFlow1, oldFmtFlow2)), nil)

	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))

	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 2)

	cp := c.checkpoints.get("node-a")
	s.Empty(cp.LastRotationID)
	s.Equal(types.UID("uid-a"), cp.NodeUID)
}

// TestBootstrap_ExistingRotationsNotReplayed: historical .gz backups already exist
// at first sight -> they are NOT read; LastRotationID is set to the newest present.
func (s *AutoModeClientTestSuite) TestBootstrap_ExistingRotationsNotReplayed() {
	ctx := context.Background()
	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	c := s.newClient()

	// Two historical rotations already present at bootstrap.
	s.fetcher.queueRotations("node-a", []RotatedFile{
		{ID: "2026-08-07T10-00-00.000", Filename: "network-policy-agent-2026-08-07T10-00-00.000.log.gz", Compressed: true},
		{ID: "2026-08-07T11-00-00.000", Filename: "network-policy-agent-2026-08-07T11-00-00.000.log.gz", Compressed: true},
	})
	// If bootstrap wrongly replayed these, CacheFlow would fire for them.
	s.fetcher.setRotatedData("node-a", "network-policy-agent-2026-08-07T10-00-00.000.log.gz", gzipBytes(flowLines(oldFmtFlow1)))
	s.fetcher.setRotatedData("node-a", "network-policy-agent-2026-08-07T11-00-00.000.log.gz", gzipBytes(flowLines(oldFmtFlow2)))

	// Active file has a single fresh flow.
	s.fetcher.queue("node-a", []byte(flowLines(oldFmtFlow3)), nil)

	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))

	// Only the active flow processed; backups skipped.
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 1)
	s.Equal("2026-08-07T11-00-00.000", c.checkpoints.get("node-a").LastRotationID)
}

// TestNormalAppend_NoRotation: after bootstrap, an append with no new rotation
// only emits the newly-appended record.
func (s *AutoModeClientTestSuite) TestNormalAppend_NoRotation() {
	ctx := context.Background()
	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	c := s.newClient()

	// Bootstrap poll (no rotations).
	s.fetcher.queue("node-a", []byte(flowLines(oldFmtFlow1)), nil)
	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 1)

	// Second poll: append one line, still no rotations.
	s.fetcher.queue("node-a", []byte(flowLines(oldFmtFlow1, oldFmtFlow2)), nil)
	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 2)
}

// TestSingleRotation_GzipRecoveryUsesOffset: after processing the active file to
// some offset, it rotates to a .gz; recovery decompresses, skips the already-read
// prefix, and processes only the tail, then reads the new active file from zero.
func (s *AutoModeClientTestSuite) TestSingleRotation_GzipRecoveryUsesOffset() {
	ctx := context.Background()
	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	c := s.newClient()

	// Bootstrap: active file has flow1 + flow2. Offset advances to end of both.
	activePrefix := flowLines(oldFmtFlow1, oldFmtFlow2)
	s.fetcher.queue("node-a", []byte(activePrefix), nil)
	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 2)

	offsetAfterBootstrap := c.checkpoints.get("node-a").ByteOffset
	s.Equal(len(activePrefix), offsetAfterBootstrap)

	// Now the file rotated: the old active file (prefix + a NEW tail flow3) became
	// R11.gz, and a fresh active file (flow with only flow1 again) exists.
	rotatedContent := activePrefix + flowLines(oldFmtFlow3) // tail flow3 was never read

	s.fetcher.queueRotations("node-a", []RotatedFile{
		{ID: "2026-08-07T11-00-00.000", Filename: "network-policy-agent-2026-08-07T11-00-00.000.log.gz", Compressed: true},
	})
	s.fetcher.setRotatedData("node-a", "network-policy-agent-2026-08-07T11-00-00.000.log.gz", gzipBytes(rotatedContent))

	// Fresh active file after rotation: one brand-new flow.
	s.fetcher.queue("node-a", []byte(flowLines(oldFmtFlow2)), nil)

	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))

	// Recovery should emit ONLY the tail flow3 (prefix skipped) + the fresh active
	// flow2 = 2 more CacheFlow calls (total 4). Not the whole rotated file.
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 4)

	cp := c.checkpoints.get("node-a")
	s.Equal("2026-08-07T11-00-00.000", cp.LastRotationID)
}

// TestMultipleRotations_OnlyFirstUsesOffset: three unseen rotations at once. Only
// the oldest resumes from the prior offset; the rest are read whole.
func (s *AutoModeClientTestSuite) TestMultipleRotations_OnlyFirstUsesOffset() {
	ctx := context.Background()
	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	c := s.newClient()

	// Bootstrap: active file with flow1 only (offset -> end of flow1 line).
	prefix := flowLines(oldFmtFlow1)
	s.fetcher.queue("node-a", []byte(prefix), nil)
	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 1)

	// Three rotations appear. R11 = old active (prefix + flow2 tail). R12, R13 new.
	s.fetcher.queueRotations("node-a", []RotatedFile{
		{ID: "2026-08-07T11-00-00.000", Filename: "r11.log.gz", Compressed: true},
		{ID: "2026-08-07T12-00-00.000", Filename: "r12.log.gz", Compressed: true},
		{ID: "2026-08-07T13-00-00.000", Filename: "r13.log.gz", Compressed: true},
	})
	s.fetcher.setRotatedData("node-a", "r11.log.gz", gzipBytes(prefix+flowLines(oldFmtFlow2))) // tail flow2 new
	s.fetcher.setRotatedData("node-a", "r12.log.gz", gzipBytes(flowLines(oldFmtFlow3)))        // whole file new
	s.fetcher.setRotatedData("node-a", "r13.log.gz", gzipBytes(flowLines(oldFmtFlow1)))        // whole file new
	s.fetcher.queue("node-a", []byte(flowLines(oldFmtFlow2)), nil)                             // fresh active

	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))

	// R11 tail (1) + R12 (1) + R13 (1) + active (1) = 4 more, total 5.
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 5)
	s.Equal("2026-08-07T13-00-00.000", c.checkpoints.get("node-a").LastRotationID)
}

// TestCompressionRace_LogRenamedToGz: listing offers R11.log, but open() 404s
// because it was compressed to R11.log.gz between list and fetch. Recovery
// re-lists, finds the .gz, and processes it.
func (s *AutoModeClientTestSuite) TestCompressionRace_LogRenamedToGz() {
	ctx := context.Background()
	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	c := s.newClient()

	// Bootstrap with an empty active file so offset is 0.
	s.fetcher.queue("node-a", []byte(""), nil)
	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))

	// First list returns the uncompressed .log; second (re-list) returns the .gz.
	s.fetcher.queueRotations("node-a", []RotatedFile{
		{ID: "2026-08-07T11-00-00.000", Filename: "network-policy-agent-2026-08-07T11-00-00.000.log", Compressed: false},
	})
	s.fetcher.queueRotations("node-a", []RotatedFile{
		{ID: "2026-08-07T11-00-00.000", Filename: "network-policy-agent-2026-08-07T11-00-00.000.log.gz", Compressed: true},
	})
	// Only the .gz has data; the .log is absent (open -> errRotatedFileNotFound).
	s.fetcher.setRotatedData("node-a", "network-policy-agent-2026-08-07T11-00-00.000.log.gz", gzipBytes(flowLines(oldFmtFlow1)))
	s.fetcher.queue("node-a", []byte(""), nil) // fresh active empty

	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))

	// The single flow from the recovered .gz is processed.
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 1)
	s.Equal("2026-08-07T11-00-00.000", c.checkpoints.get("node-a").LastRotationID)
}

// TestBothPresent_PrefersUncompressedLog: when a rotation's ".log" and ".log.gz"
// are both listed (lumberjack still mid-compression), the uncompressed ".log" is
// read and the ".log.gz" is never opened, because the ".gz" may still be truncated
// until compression completes and lumberjack removes the ".log".
func (s *AutoModeClientTestSuite) TestBothPresent_PrefersUncompressedLog() {
	ctx := context.Background()
	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	c := s.newClient()

	// Bootstrap with an empty active file so offset is 0.
	s.fetcher.queue("node-a", []byte(""), nil)
	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))

	logName := "network-policy-agent-2026-08-07T11-00-00.000.log"
	gzName := "network-policy-agent-2026-08-07T11-00-00.000.log.gz"

	// Both forms of the same generation are present (compression in progress).
	s.fetcher.queueRotations("node-a", []RotatedFile{
		{ID: "2026-08-07T11-00-00.000", Filename: logName, Compressed: false},
		{ID: "2026-08-07T11-00-00.000", Filename: gzName, Compressed: true},
	})
	// The ".log" holds the complete data; the ".gz" is registered with DIFFERENT
	// data so that a mistaken read of the ".gz" would change the observed result.
	s.fetcher.setRotatedData("node-a", logName, []byte(flowLines(oldFmtFlow1)))
	s.fetcher.setRotatedData("node-a", gzName, gzipBytes(flowLines(oldFmtFlow2, oldFmtFlow3)))
	s.fetcher.queue("node-a", []byte(""), nil) // fresh active empty

	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))

	// Exactly one flow (from the ".log") is processed; the ".gz" is never opened.
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 1)
	s.Equal(1, s.fetcher.openCalls["node-a|"+logName])
	s.Equal(0, s.fetcher.openCalls["node-a|"+gzName])
	s.Equal("2026-08-07T11-00-00.000", c.checkpoints.get("node-a").LastRotationID)
}

// TestMissingRotation_RecoveryGap: a rotation is listed but neither .log nor .gz
// is fetchable (retention deleted it). Recovery must not error or claim success;
// it advances past the gap and continues.
func (s *AutoModeClientTestSuite) TestMissingRotation_RecoveryGap() {
	ctx := context.Background()
	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	c := s.newClient()

	s.fetcher.queue("node-a", []byte(""), nil)
	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))

	// Rotation listed but no data registered for it, and the re-list still has no
	// resolvable file -> genuine gap.
	s.fetcher.queueRotations("node-a", []RotatedFile{
		{ID: "2026-08-07T11-00-00.000", Filename: "network-policy-agent-2026-08-07T11-00-00.000.log", Compressed: false},
	})
	s.fetcher.queueRotations("node-a", []RotatedFile{}) // re-list finds nothing
	s.fetcher.queue("node-a", []byte(flowLines(oldFmtFlow1)), nil)

	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))

	// The gap generation contributes nothing, but the fresh active flow is read.
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 1)
	// Checkpoint advanced past the gap so we don't re-attempt it forever.
	s.Equal("2026-08-07T11-00-00.000", c.checkpoints.get("node-a").LastRotationID)
}

// TestNodeUIDChange_Rebootstraps: a UID change routes back through bootstrap
// (fresh start, no replay of pre-existing rotations).
func (s *AutoModeClientTestSuite) TestNodeUIDChange_Rebootstraps() {
	ctx := context.Background()
	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	c := s.newClient()

	s.fetcher.queue("node-a", []byte(flowLines(oldFmtFlow1)), nil)
	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 1)

	// New UID -> bootstrap again. A rotation is present but must NOT be replayed.
	s.fetcher.queueRotations("node-a", []RotatedFile{
		{ID: "2026-08-07T11-00-00.000", Filename: "r11.log.gz", Compressed: true},
	})
	s.fetcher.setRotatedData("node-a", "r11.log.gz", gzipBytes(flowLines(oldFmtFlow3)))
	s.fetcher.queue("node-a", []byte(flowLines(oldFmtFlow2)), nil)

	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-b"), s.logger))

	// Only the fresh active flow2; the rotation is recorded, not replayed.
	s.mockSink.AssertNumberOfCalls(s.T(), "CacheFlow", 2)

	cp := c.checkpoints.get("node-a")
	s.Equal(types.UID("uid-b"), cp.NodeUID)
	s.Equal("2026-08-07T11-00-00.000", cp.LastRotationID)
}

// TestGzipCorruption_DoesNotAdvance: a rotated .gz that fails to decompress
// returns an error from pollNode and does NOT advance LastRotationID.
func (s *AutoModeClientTestSuite) TestGzipCorruption_DoesNotAdvance() {
	ctx := context.Background()
	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	c := s.newClient()

	s.fetcher.queue("node-a", []byte(""), nil)
	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))

	s.fetcher.queueRotations("node-a", []RotatedFile{
		{ID: "2026-08-07T11-00-00.000", Filename: "r11.log.gz", Compressed: true},
	})
	// Not valid gzip bytes.
	s.fetcher.setRotatedData("node-a", "r11.log.gz", []byte("not-gzip-data"))

	err := c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger)
	s.Require().Error(err)

	// LastRotationID unchanged (still bootstrap's "").
	s.Empty(c.checkpoints.get("node-a").LastRotationID)
}

// TestListError_DoesNotAdvance: a directory list failure on a normal poll returns
// an error and leaves the checkpoint untouched.
func (s *AutoModeClientTestSuite) TestListError_DoesNotAdvance() {
	ctx := context.Background()
	s.mockSink.On("CacheFlow", ctx, mock.Anything).Return(nil)
	s.mockSink.On("IncrementFlowsReceived").Return()

	c := s.newClient()

	// Bootstrap OK.
	s.fetcher.queue("node-a", []byte(flowLines(oldFmtFlow1)), nil)
	s.Require().NoError(c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger))
	offset := c.checkpoints.get("node-a").ByteOffset

	// Replace fetcher with one whose list() errors on the next poll.
	c.fetcher = &listErrFetcher{inner: s.fetcher}

	err := c.pollNode(ctx, "node-a", types.UID("uid-a"), s.logger)
	s.Require().Error(err)
	s.Equal(offset, c.checkpoints.get("node-a").ByteOffset)
}

// listErrFetcher wraps a fetcher and forces list() to fail, to exercise the
// list-error path in a post-bootstrap poll.
type listErrFetcher struct {
	inner nodeLogFetcher
}

func (l *listErrFetcher) fetchActive(ctx context.Context, n string, rangeStart int64) (*activeStream, error) {
	return l.inner.fetchActive(ctx, n, rangeStart)
}

func (l *listErrFetcher) list(context.Context, string) ([]RotatedFile, error) {
	return nil, context.DeadlineExceeded
}

func (l *listErrFetcher) open(ctx context.Context, n, f string) (io.ReadCloser, error) {
	return l.inner.open(ctx, n, f)
}
