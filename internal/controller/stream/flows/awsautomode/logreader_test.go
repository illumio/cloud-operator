// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsautomode

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
)

// linesToStrings is a test helper to compare returned [][]byte as strings.
func linesToStrings(lines [][]byte) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, string(l))
	}

	return out
}

func TestReconcile_FirstFetchReturnsAllCompleteLines(t *testing.T) {
	cp := &nodeLogCheckpoint{NodeName: "n1"}
	data := []byte("a\nb\nc\n")

	slice := reconcile(cp, data, types.UID("uid-1"))

	assert.False(t, slice.reset)
	assert.Equal(t, []string{"a", "b", "c"}, linesToStrings(slice.lines))
	assert.Equal(t, len(data), slice.next.ByteOffset)
	assert.Equal(t, len(data), slice.next.LastObservedSize)
	assert.Equal(t, types.UID("uid-1"), slice.next.NodeUID)
	assert.Equal(t, hashRecord([]byte("c")), slice.next.LastRecordHash)
}

func TestReconcile_TrailingPartialLineHeldBack(t *testing.T) {
	cp := &nodeLogCheckpoint{NodeName: "n1"}
	data := []byte("a\nb\npartial")

	slice := reconcile(cp, data, types.UID("uid-1"))

	// Only complete lines; "partial" is not returned and offset stops at last newline.
	assert.Equal(t, []string{"a", "b"}, linesToStrings(slice.lines))
	assert.Equal(t, 4, slice.next.ByteOffset) // after "a\nb\n"
	assert.Equal(t, len(data), slice.next.LastObservedSize)
}

func TestReconcile_IncrementalAppendReturnsOnlyNew(t *testing.T) {
	data1 := []byte("a\nb\nc\n")
	cp := &nodeLogCheckpoint{NodeName: "n1"}
	s1 := reconcile(cp, data1, types.UID("uid-1"))
	next := s1.next

	// Second fetch: same content plus new lines.
	data2 := []byte("a\nb\nc\nd\ne\n")
	s2 := reconcile(&next, data2, types.UID("uid-1"))

	assert.False(t, s2.reset)
	assert.Equal(t, []string{"d", "e"}, linesToStrings(s2.lines))
	assert.Equal(t, len(data2), s2.next.ByteOffset)
	assert.Equal(t, hashRecord([]byte("e")), s2.next.LastRecordHash)
}

func TestReconcile_PartialLineCompletedNextFetch(t *testing.T) {
	cp := &nodeLogCheckpoint{NodeName: "n1"}
	s1 := reconcile(cp, []byte("a\nb\npar"), types.UID("uid-1"))
	assert.Equal(t, []string{"a", "b"}, linesToStrings(s1.lines))
	next := s1.next

	// The partial "par" is now completed to "partial\n".
	s2 := reconcile(&next, []byte("a\nb\npartial\n"), types.UID("uid-1"))
	assert.False(t, s2.reset)
	assert.Equal(t, []string{"partial"}, linesToStrings(s2.lines))
}

func TestReconcile_TruncationResetsOffset(t *testing.T) {
	data1 := []byte("a\nb\nc\nd\n")
	cp := &nodeLogCheckpoint{NodeName: "n1"}
	s1 := reconcile(cp, data1, types.UID("uid-1"))
	next := s1.next

	// File shrank (truncated/rotated to smaller). Reprocess from start.
	data2 := []byte("x\ny\n")
	s2 := reconcile(&next, data2, types.UID("uid-1"))

	assert.True(t, s2.reset)
	assert.Equal(t, []string{"x", "y"}, linesToStrings(s2.lines))
	assert.Equal(t, len(data2), s2.next.ByteOffset)
}

func TestReconcile_RotationWithoutShrinkResetsOffset(t *testing.T) {
	data1 := []byte("a\nb\nc\n")
	cp := &nodeLogCheckpoint{NodeName: "n1"}
	s1 := reconcile(cp, data1, types.UID("uid-1"))
	next := s1.next

	// Same or larger size, but content at the boundary differs: rotation replaced
	// the file. The record ending at the stored offset no longer matches the hash.
	data2 := []byte("w\nx\ny\nz\n")
	s2 := reconcile(&next, data2, types.UID("uid-1"))

	assert.True(t, s2.reset)
	assert.Equal(t, []string{"w", "x", "y", "z"}, linesToStrings(s2.lines))
}

func TestReconcile_NodeUIDChangeResetsOffset(t *testing.T) {
	data1 := []byte("a\nb\nc\n")
	cp := &nodeLogCheckpoint{NodeName: "n1"}
	s1 := reconcile(cp, data1, types.UID("uid-1"))
	next := s1.next

	// Node object replaced (new UID). Even with identical bytes, reprocess.
	s2 := reconcile(&next, data1, types.UID("uid-2"))

	assert.True(t, s2.reset)
	assert.Equal(t, []string{"a", "b", "c"}, linesToStrings(s2.lines))
	assert.Equal(t, types.UID("uid-2"), s2.next.NodeUID)
}

func TestReconcile_NoNewDataReturnsNoLines(t *testing.T) {
	data1 := []byte("a\nb\n")
	cp := &nodeLogCheckpoint{NodeName: "n1"}
	s1 := reconcile(cp, data1, types.UID("uid-1"))
	next := s1.next

	// Identical fetch: nothing new.
	s2 := reconcile(&next, data1, types.UID("uid-1"))

	assert.False(t, s2.reset)
	assert.Empty(t, s2.lines)
	assert.Equal(t, next.ByteOffset, s2.next.ByteOffset)
}

func TestReconcile_EmptyData(t *testing.T) {
	cp := &nodeLogCheckpoint{NodeName: "n1"}
	s := reconcile(cp, nil, types.UID("uid-1"))

	assert.False(t, s.reset)
	assert.Empty(t, s.lines)
	assert.Equal(t, 0, s.next.ByteOffset)
}

func TestRecordEndingAtMatches(t *testing.T) {
	data := []byte("a\nbb\nccc\n")
	// record "bb" ends at index 5 (data[4]=='\n', end==5).
	require.Equal(t, byte('\n'), data[4])
	assert.True(t, recordEndingAtMatches(data, 5, hashRecord([]byte("bb"))))
	assert.False(t, recordEndingAtMatches(data, 5, hashRecord([]byte("xx"))))
	// end not at a newline boundary.
	assert.False(t, recordEndingAtMatches(data, 4, hashRecord([]byte("bb"))))
	// out of range.
	assert.False(t, recordEndingAtMatches(data, 100, hashRecord([]byte("bb"))))
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
