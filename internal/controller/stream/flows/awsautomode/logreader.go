// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsautomode

import (
	"bytes"
	"crypto/md5" //nolint:gosec // non-cryptographic use: same-file continuation check, no security requirement

	"k8s.io/apimachinery/pkg/types"
)

// logSlice is the result of reconciling a freshly-fetched log against a node's
// checkpoint. It describes which complete lines are new and what the checkpoint
// should become once those lines have been successfully processed.
type logSlice struct {
	// lines are the complete (newline-terminated in the source) log lines that
	// have not yet been processed, in order.
	lines [][]byte
	// reset is true if a truncation, rotation, or node replacement was detected
	// and processing restarted from the beginning of the file.
	reset bool
	// next holds the checkpoint values to persist AFTER lines are processed.
	next nodeLogCheckpoint
}

// reconcile compares a freshly fetched log (data) against the prior checkpoint
// (cp) for a node whose current object UID is nodeUID, and returns the new
// complete lines to process plus the checkpoint to persist afterwards.
//
// Detection rules:
//   - Node replaced (UID changed): restart from offset 0.
//   - Truncation/rotation-with-shrink (data smaller than last observed size):
//     restart from offset 0.
//   - Rotation-without-shrink (offset still in range but the record ending at the
//     offset no longer matches LastRecordHash): restart from offset 0.
//   - Otherwise: resume at the stored ByteOffset.
//
// A trailing partial line (bytes after the final newline) is NOT returned; the
// offset stops at the last newline so the partial line is re-read, and completed,
// on the next poll.
func reconcile(cp *nodeLogCheckpoint, data []byte, nodeUID types.UID) logSlice {
	start := cp.ByteOffset
	reset := false

	switch {
	case cp.NodeUID != "" && cp.NodeUID != nodeUID:
		// Node object replaced; any prior offset refers to a different file.
		reset = true
	case len(data) < cp.LastObservedSize:
		// File shrank: truncated or rotated to a smaller file.
		reset = true
	case start > len(data):
		// Offset past end (shouldn't happen given the size check, but be safe).
		reset = true
	case start > 0 && !recordEndingAtMatches(data, start, cp.LastRecordHash):
		// Same-or-larger size but the record boundary no longer matches: the file
		// was replaced (rotated) with different content.
		reset = true
	}

	if reset {
		start = 0
	}

	// Advance only through the last newline; keep any trailing partial line.
	lastNL := bytes.LastIndexByte(data, '\n')
	next := nodeLogCheckpoint{
		NodeName:         cp.NodeName,
		NodeUID:          nodeUID,
		ByteOffset:       cp.ByteOffset,
		LastObservedSize: len(data),
		LastRecordHash:   cp.LastRecordHash,
		// LastRotationID is owned by the rotation-recovery path, not by active-file
		// reconcile; carry it through unchanged so a normal poll never clears it.
		LastRotationID: cp.LastRotationID,
	}

	if reset {
		next.ByteOffset = 0
	}

	if lastNL < 0 || lastNL < start {
		// No complete new line beyond the start offset.
		next.ByteOffset = maxInt(start, next.ByteOffset)
		if reset {
			next.ByteOffset = 0
		}

		return logSlice{lines: nil, reset: reset, next: next}
	}

	// complete region is data[start : lastNL+1]; split into lines dropping empties.
	region := data[start : lastNL+1]
	rawLines := bytes.Split(region, []byte{'\n'})

	lines := make([][]byte, 0, len(rawLines))

	var lastLine []byte

	for _, l := range rawLines {
		if len(l) == 0 {
			continue
		}
		// Copy: the underlying data buffer is transient across polls.
		lineCopy := append([]byte(nil), l...)
		lines = append(lines, lineCopy)
		lastLine = lineCopy
	}

	next.ByteOffset = lastNL + 1
	if lastLine != nil {
		next.LastRecordHash = hashRecord(lastLine)
	}

	return logSlice{lines: lines, reset: reset, next: next}
}

// recordEndingAtMatches reports whether the log record that ends at byte index
// end (end points just past a '\n') hashes to want. This confirms the file we
// just fetched is a continuation of the one behind the stored offset.
func recordEndingAtMatches(data []byte, end int, want [md5.Size]byte) bool {
	if end <= 0 || end > len(data) {
		return false
	}

	// The newline for the previous record sits at end-1.
	if data[end-1] != '\n' {
		return false
	}

	// Find the start of that record: the byte after the preceding newline.
	prevNL := bytes.LastIndexByte(data[:end-1], '\n')
	recStart := prevNL + 1 // -1 -> 0 when there is no earlier newline.
	record := data[recStart : end-1]

	return hashRecord(record) == want
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}

	return b
}
