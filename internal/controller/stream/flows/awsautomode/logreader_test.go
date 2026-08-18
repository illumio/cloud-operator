// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsautomode

import (
	"bufio"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collectEmit returns an emit func that appends each record (as a string) to out.
func collectEmit(out *[]string) func([]byte) error {
	return func(rec []byte) error {
		*out = append(*out, string(rec))

		return nil
	}
}

func TestStreamActive_FirstReadEmitsAllRecords(t *testing.T) {
	cp := &nodeLogCheckpoint{NodeName: "n1"}
	data := "a\nb\nc\n"

	var got []string

	res, err := streamActive(0, strings.NewReader(data), cp, collectEmit(&got))
	require.NoError(t, err)

	assert.False(t, res.needReset)
	assert.True(t, res.validated)
	assert.Equal(t, []string{"a", "b", "c"}, got)
	assert.Equal(t, len(data), res.newOffset)
	assert.Equal(t, len(data), res.observedSize)
	assert.Equal(t, hashRecord([]byte("c")), res.lastHash)
}

func TestStreamActive_TrailingPartialHeldBack(t *testing.T) {
	cp := &nodeLogCheckpoint{NodeName: "n1"}
	data := "a\nb\npartial"

	var got []string

	res, err := streamActive(0, strings.NewReader(data), cp, collectEmit(&got))
	require.NoError(t, err)

	// "partial" has no terminating newline: not emitted, offset stops after "a\nb\n".
	assert.Equal(t, []string{"a", "b"}, got)
	assert.Equal(t, 4, res.newOffset)
	assert.Equal(t, len(data), res.observedSize)
	assert.False(t, res.needReset)
}

func TestStreamActive_WholeFileIncrementalAppendEmitsOnlyNew(t *testing.T) {
	// First read establishes the checkpoint.
	first := "a\nb\nc\n"

	var got1 []string

	r1, err := streamActive(0, strings.NewReader(first), &nodeLogCheckpoint{NodeName: "n1"}, collectEmit(&got1))
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, got1)

	cp := &nodeLogCheckpoint{NodeName: "n1", ByteOffset: r1.newOffset, LastRecordHash: r1.lastHash}

	// Second read (HTTP 200 whole file): same prefix plus two new records.
	second := "a\nb\nc\nd\ne\n"

	var got2 []string

	r2, err := streamActive(0, strings.NewReader(second), cp, collectEmit(&got2))
	require.NoError(t, err)

	assert.False(t, r2.needReset)
	assert.True(t, r2.validated)
	assert.Equal(t, []string{"d", "e"}, got2)
	assert.Equal(t, len(second), r2.newOffset)
	assert.Equal(t, hashRecord([]byte("e")), r2.lastHash)
}

func TestStreamActive_RangeContinuationEmitsOnlyNew(t *testing.T) {
	// Full file is "x\ny\nz\n" (offset 6). A 206 begins mid-file at bodyStart=2 (the
	// start of "y"), overlapping the boundary record "z" so it can be re-validated.
	cp := &nodeLogCheckpoint{NodeName: "n1", ByteOffset: 6, LastRecordHash: hashRecord([]byte("z"))}

	body := "y\nz\nd\n" // bytes 2.. of "x\ny\nz\nd\n"

	var got []string

	res, err := streamActive(2, strings.NewReader(body), cp, collectEmit(&got))
	require.NoError(t, err)

	assert.False(t, res.needReset)
	assert.True(t, res.validated)
	assert.Equal(t, []string{"d"}, got)
	assert.Equal(t, 8, res.newOffset)
	assert.Equal(t, hashRecord([]byte("d")), res.lastHash)
}

func TestStreamActive_RangeBoundaryMismatchResets(t *testing.T) {
	// The record ending at the checkpoint no longer hashes to LastRecordHash (the
	// file was replaced by rotation). Reset without emitting anything.
	cp := &nodeLogCheckpoint{NodeName: "n1", ByteOffset: 6, LastRecordHash: hashRecord([]byte("z"))}

	body := "y\nZ\nd\n" // boundary record is "Z", not "z"

	var got []string

	res, err := streamActive(2, strings.NewReader(body), cp, collectEmit(&got))
	require.NoError(t, err)

	assert.True(t, res.needReset)
	assert.Empty(t, got)
}

func TestStreamActive_ShrinkBelowOffsetResets(t *testing.T) {
	// Whole-file (200) read of a file that shrank below the checkpoint offset: the
	// boundary record is never re-read, so reset without emitting.
	cp := &nodeLogCheckpoint{NodeName: "n1", ByteOffset: 6, LastRecordHash: hashRecord([]byte("z"))}

	var got []string

	res, err := streamActive(0, strings.NewReader("a\n"), cp, collectEmit(&got))
	require.NoError(t, err)

	assert.True(t, res.needReset)
	assert.Empty(t, got)
}

func TestStreamActive_OverlapTooSmallResets(t *testing.T) {
	// bodyStart lands so far into the file that the first newline is already past the
	// checkpoint offset: the boundary record cannot be validated, so reset.
	full := "aaa\nbbbbbbb\n" // "aaa" ends at offset 4; "bbbbbbb" spans 4..12
	cp := &nodeLogCheckpoint{NodeName: "n1", ByteOffset: 4, LastRecordHash: hashRecord([]byte("aaa"))}

	body := full[5:] // "bbbbbb\n": first record ends past offset 4

	var got []string

	res, err := streamActive(5, strings.NewReader(body), cp, collectEmit(&got))
	require.NoError(t, err)

	assert.True(t, res.needReset)
	assert.Empty(t, got)
}

func TestStreamActive_NoNewDataEmitsNothing(t *testing.T) {
	cp := &nodeLogCheckpoint{NodeName: "n1", ByteOffset: 4, LastRecordHash: hashRecord([]byte("b"))}

	// Whole file unchanged since last poll: boundary validates, nothing new.
	var got []string

	res, err := streamActive(0, strings.NewReader("a\nb\n"), cp, collectEmit(&got))
	require.NoError(t, err)

	assert.False(t, res.needReset)
	assert.True(t, res.validated)
	assert.Empty(t, got)
	assert.Equal(t, 4, res.newOffset)
}

func TestStreamActive_EmptyBody(t *testing.T) {
	cp := &nodeLogCheckpoint{NodeName: "n1"}

	var got []string

	res, err := streamActive(0, strings.NewReader(""), cp, collectEmit(&got))
	require.NoError(t, err)

	assert.False(t, res.needReset)
	assert.Empty(t, got)
	assert.Equal(t, 0, res.newOffset)
	assert.Equal(t, 0, res.observedSize)
}

func TestStreamActive_EmitErrorPropagates(t *testing.T) {
	cp := &nodeLogCheckpoint{NodeName: "n1"}

	sentinel := io.ErrClosedPipe
	emit := func([]byte) error { return sentinel }

	_, err := streamActive(0, strings.NewReader("a\nb\n"), cp, emit)
	assert.ErrorIs(t, err, sentinel)
}

func TestScanRecord_SplitsRecordsAndTracksOffsets(t *testing.T) {
	br := bufio.NewReader(strings.NewReader("ab\ncde\nf"))

	rec, consumed, hasNL, err := scanRecord(br)
	require.NoError(t, err)
	assert.Equal(t, "ab", string(rec))
	assert.Equal(t, 3, consumed)
	assert.True(t, hasNL)

	rec, consumed, hasNL, err = scanRecord(br)
	require.NoError(t, err)
	assert.Equal(t, "cde", string(rec))
	assert.Equal(t, 4, consumed)
	assert.True(t, hasNL)

	// Trailing fragment with no newline: reported with hasNL=false and io.EOF.
	rec, consumed, hasNL, err = scanRecord(br)
	require.ErrorIs(t, err, io.EOF)
	assert.Equal(t, "f", string(rec))
	assert.Equal(t, 1, consumed)
	assert.False(t, hasNL)
}

func TestScanRecord_OversizeRecordConsumedButDropped(t *testing.T) {
	// A record longer than maxRecoveryScanLine is consumed (offset stays correct) but
	// its bytes are dropped so no unbounded allocation occurs.
	big := strings.Repeat("x", maxRecoveryScanLine+10)
	br := bufio.NewReader(strings.NewReader(big + "\n" + "ok\n"))

	rec, consumed, hasNL, err := scanRecord(br)
	require.NoError(t, err)
	assert.True(t, hasNL)
	assert.Nil(t, rec)
	assert.Equal(t, len(big)+1, consumed)

	rec, _, hasNL, err = scanRecord(br)
	require.NoError(t, err)
	assert.True(t, hasNL)
	assert.Equal(t, "ok", string(rec))
}

func TestCheckpointStore_RetainAndGet(t *testing.T) {
	s := newCheckpointStore()
	s.set("n1", &nodeLogCheckpoint{NodeName: "n1", ByteOffset: 5})
	s.set("n2", &nodeLogCheckpoint{NodeName: "n2", ByteOffset: 7})
	s.set("n3", &nodeLogCheckpoint{NodeName: "n3"})

	removed := s.retain(map[string]bool{"n1": true, "n2": true})

	assert.ElementsMatch(t, []string{"n3"}, removed)
	assert.Equal(t, 5, s.get("n1").ByteOffset)
	assert.Equal(t, 7, s.get("n2").ByteOffset)
	// get() recreates a zero checkpoint for a since-removed node.
	assert.Equal(t, 0, s.get("n3").ByteOffset)
}
