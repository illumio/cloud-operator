// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsautomode

import (
	"bufio"
	"crypto/md5" //nolint:gosec // non-cryptographic use: same-file continuation check, no security requirement
	"errors"
	"io"
)

// streamActiveResult is the outcome of streaming the active log for one poll. It
// carries the checkpoint values to persist AFTER the emitted records have been
// processed downstream, plus whether the file must be reprocessed from the start.
type streamActiveResult struct {
	// validated is true once the record ending at the prior ByteOffset has been
	// confirmed to still hash to LastRecordHash (or when there was nothing to
	// validate because the prior ByteOffset was 0). New records are only emitted
	// after validation, so a failed continuation never double-sends data.
	validated bool
	// needReset is true when the active file is not a continuation of the one
	// behind the checkpoint (truncated, rotated to a smaller/replaced file, or the
	// resume offset fell beyond EOF). The caller reprocesses the whole file from 0.
	needReset bool
	// newOffset is the absolute byte offset just past the last complete record
	// emitted (or the prior ByteOffset when nothing new was seen). A trailing
	// partial line is intentionally excluded so it is re-read, and completed, next
	// poll.
	newOffset int
	// lastHash is the MD5 of the last complete record emitted (or the prior
	// LastRecordHash when nothing new was seen).
	lastHash [md5.Size]byte
	// observedSize is the highest absolute offset seen in the stream (bytes streamed
	// plus the body's start offset), i.e. the current file size when the body was
	// read to EOF.
	observedSize int
	// emitted counts how many complete records were handed to emit (including
	// non-flow lines, which emit skips); used only for debug logging.
	emitted int
}

// streamActive reads the active-log body (starting at absolute offset bodyStart)
// one record at a time, validates that it continues the file behind cp, and calls
// emit for each complete record at or beyond cp.ByteOffset. It never buffers the
// whole file: at most one record (bounded by maxRecoveryScanLine) is held at a
// time, so a ~200MB active file doesn't have to be materialized in memory.
//
// bodyStart is the absolute file offset at which the body begins: cp.ByteOffset
// minus the validation overlap for a Range hit (HTTP 206), or 0 when the whole
// file is streamed (HTTP 200, e.g. a from-scratch read). When bodyStart > 0 the
// first physical line is the tail of a record that began before bodyStart, so it
// is discarded; record boundaries are trusted only after the first newline.
//
// Emission is gated on validation: because records are read in order, the record
// ending exactly at cp.ByteOffset (whose MD5 must equal cp.LastRecordHash) is
// always seen before any newer record. If that boundary record is missing or its
// hash differs, the file was replaced or shrank, so streamActive stops and reports
// needReset WITHOUT having emitted anything, and the caller reprocesses from 0.
func streamActive(bodyStart int, r io.Reader, cp *nodeLogCheckpoint, emit func([]byte) error) (streamActiveResult, error) {
	br := bufio.NewReaderSize(r, 64*1024)

	abs := bodyStart

	res := streamActiveResult{
		validated:    cp.ByteOffset == 0,
		newOffset:    cp.ByteOffset,
		lastHash:     cp.LastRecordHash,
		observedSize: bodyStart,
	}

	firstRecord := true

	for {
		rec, consumed, hasNL, err := scanRecord(br)

		abs += consumed
		if consumed > 0 {
			res.observedSize = abs
		}

		// A trailing partial line (no newline) does not advance newOffset; it is left
		// for the next poll to complete. Only complete records (hasNL) are applied.
		if hasNL {
			done, e := applyRecord(rec, abs, bodyStart, &firstRecord, cp, &res, emit)
			if e != nil {
				return res, e
			}

			if done {
				return res, nil
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return res, err
		}
	}

	// Resuming from a non-zero offset but never re-reading its boundary record means
	// the file shrank below that offset (truncation/rotation): reprocess from 0.
	if cp.ByteOffset > 0 && !res.validated {
		res.needReset = true
	}

	return res, nil
}

// applyRecord classifies one complete record (ending at absolute offset end) during
// streamActive and updates res / firstRecord accordingly. It returns done=true when
// streaming must stop early because a reset was detected, or a non-nil error when
// emit fails. Records are seen in order, so the boundary record ending exactly at
// cp.ByteOffset is always classified before any newer record.
func applyRecord(rec []byte, end, bodyStart int, firstRecord *bool, cp *nodeLogCheckpoint, res *streamActiveResult, emit func([]byte) error) (bool, error) {
	first := *firstRecord
	*firstRecord = false

	switch {
	case first && bodyStart > 0:
		// Untrusted leading fragment (bodyStart landed mid-record). Discard it. If it
		// already extends past the resume offset, the overlap was too small to re-read
		// the boundary record, so we cannot validate the continuation: reset.
		if end > cp.ByteOffset {
			res.needReset = true

			return true, nil
		}

		return false, nil
	case end < cp.ByteOffset:
		// Within the already-processed region; skip.
		return false, nil
	case end == cp.ByteOffset:
		// The record ending at the resume offset: confirm continuation.
		if hashRecord(rec) != cp.LastRecordHash {
			res.needReset = true

			return true, nil
		}

		res.validated = true

		return false, nil
	default:
		// A record beyond the resume offset. If the boundary was never validated the
		// file diverged; reprocess from the start.
		if !res.validated {
			res.needReset = true

			return true, nil
		}

		if err := emit(rec); err != nil {
			return false, err
		}

		res.emitted++
		res.newOffset = end
		res.lastHash = hashRecord(rec)

		return false, nil
	}
}

// scanRecord reads the next newline-delimited record from br. It returns the
// record bytes WITHOUT the trailing newline, the number of raw bytes consumed
// (including the newline, so callers can track absolute offsets), whether a
// terminating newline was seen (false for a trailing partial line at EOF), and any
// read error (io.EOF at end of stream).
//
// A single record is capped at maxRecoveryScanLine bytes: a pathological
// missing-newline file cannot force an unbounded allocation. When a record exceeds
// the cap the surplus is consumed (so offsets stay correct) but dropped from the
// returned bytes, and the truncated record simply fails to parse as a flow.
func scanRecord(br *bufio.Reader) (rec []byte, consumed int, hasNL bool, err error) {
	var (
		buf      []byte
		oversize bool
	)

	for {
		chunk, e := br.ReadSlice('\n')
		consumed += len(chunk)

		switch {
		case errors.Is(e, bufio.ErrBufferFull):
			// Partial line filling bufio's buffer; keep reading until the newline.
			if !oversize && len(buf)+len(chunk) <= maxRecoveryScanLine {
				buf = append(buf, chunk...)
			} else {
				oversize = true
			}

			continue
		case e != nil:
			// EOF (or other error): chunk is a trailing fragment with no newline.
			if oversize || len(buf)+len(chunk) > maxRecoveryScanLine {
				return nil, consumed, false, e
			}

			buf = append(buf, chunk...)

			return buf, consumed, false, e
		}

		// chunk ends with '\n'; drop it from the returned record.
		body := chunk[:len(chunk)-1]

		if oversize || len(buf)+len(body) > maxRecoveryScanLine {
			return nil, consumed, true, nil
		}

		buf = append(buf, body...)

		return buf, consumed, true, nil
	}
}
