// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsautomode

import (
	"sort"
	"strings"
	"time"
)

// lumberjackBackupTimeFormat is lumberjack's fixed backupTimeFormat, the layout of
// the <timestamp> in a rotated file name (network-policy-agent-<timestamp>.log).
// It is fixed-width, so IDs sort lexically in chronological order.
const lumberjackBackupTimeFormat = "2006-01-02T15-04-05.000"

// RotatedFile is a single rotated generation of the Network Policy Agent log as
// produced by lumberjack. lumberjack rotates by size: when the active file
// (network-policy-agent.log) reaches its max size it is RENAMED to
// network-policy-agent-<timestamp>.log and then compressed in place to
// network-policy-agent-<timestamp>.log.gz. The timestamp uses lumberjack's
// backupTimeFormat "2006-01-02T15-04-05.000", which is fixed-width, so the raw
// string sorts lexically in chronological order and is used directly as the
// rotation ID.
type RotatedFile struct {
	// ID is the lumberjack backup timestamp (e.g. "2026-08-07T15-04-05.000"),
	// shared by the transient ".log" and the final ".log.gz" of one rotation.
	ID string
	// Filename is the actual file name to fetch via the node-proxy log endpoint.
	Filename string
	// Compressed is true when Filename ends in ".gz" and must be gunzipped.
	Compressed bool
}

// splitActiveBase splits an active log base name like "network-policy-agent.log"
// into its prefix ("network-policy-agent") and extension (".log"). lumberjack
// forms backups as prefix + "-" + timestamp + ext (+ optional ".gz").
func splitActiveBase(activeBase string) (prefix, ext string) {
	// Match lumberjack: ext is the final path extension; prefix is the remainder.
	if i := strings.LastIndex(activeBase, "."); i >= 0 {
		return activeBase[:i], activeBase[i:]
	}

	return activeBase, ""
}

// parseRotationFilename parses a directory entry name into a RotatedFile. It
// returns ok=false for names that are not a rotated generation of activeBase
// (including the active file itself, which has no timestamp). activeBase is the
// active log base name, e.g. "network-policy-agent.log".
//
// Recognized forms (prefix="network-policy-agent", ext=".log"):
//
//	network-policy-agent-2026-08-07T15-04-05.000.log
//	network-policy-agent-2026-08-07T15-04-05.000.log.gz
func parseRotationFilename(name, activeBase string) (RotatedFile, bool) {
	prefix, ext := splitActiveBase(activeBase)

	// Must start with "<prefix>-" (the dash separating prefix from timestamp).
	head := prefix + "-"
	if !strings.HasPrefix(name, head) {
		return RotatedFile{}, false
	}

	rest := name[len(head):]

	compressed := false
	if strings.HasSuffix(rest, ".gz") {
		compressed = true
		rest = strings.TrimSuffix(rest, ".gz")
	}

	// After optionally trimming .gz the remainder must end in the base ext.
	if !strings.HasSuffix(rest, ext) {
		return RotatedFile{}, false
	}

	id := strings.TrimSuffix(rest, ext)

	// The ID must be a valid lumberjack backup timestamp, not merely non-empty.
	// Accepting any suffix would let a stray file (e.g. "network-policy-agent-
	// debug.log" -> ID "debug") produce an ID that sorts lexically AFTER real
	// digit-leading timestamps, which would make every genuine rotation appear
	// already seen and silently stall rotation recovery.
	if _, err := time.Parse(lumberjackBackupTimeFormat, id); err != nil {
		return RotatedFile{}, false
	}

	return RotatedFile{ID: id, Filename: name, Compressed: compressed}, true
}

// dedupRotations collapses multiple entries with the same rotation ID (the
// transient ".log" and the final ".log.gz" of one lumberjack generation) into a
// single RotatedFile per ID. When both are present the compressed ".gz" is
// preferred, since it is the final, stable form; the uncompressed one may be
// mid-compression and disappear (see the compression race in recoverRotatedFile).
func dedupRotations(files []RotatedFile) []RotatedFile {
	byID := make(map[string]RotatedFile, len(files))

	for _, f := range files {
		existing, ok := byID[f.ID]
		if !ok {
			byID[f.ID] = f

			continue
		}
		// Prefer the compressed form when both exist for the same ID.
		if f.Compressed && !existing.Compressed {
			byID[f.ID] = f
		}
	}

	out := make([]RotatedFile, 0, len(byID))
	for _, f := range byID {
		out = append(out, f)
	}

	sortRotations(out)

	return out
}

// sortRotations orders rotations oldest-to-newest by ID. IDs are fixed-width
// lumberjack timestamps, so lexical order is chronological order.
func sortRotations(files []RotatedFile) {
	sort.Slice(files, func(i, j int) bool {
		return files[i].ID < files[j].ID
	})
}

// unseenRotations returns the deduped rotations whose ID is strictly greater than
// lastRotationID (i.e. not yet processed), sorted oldest-to-newest. An empty
// lastRotationID means every present rotation is unseen.
func unseenRotations(files []RotatedFile, lastRotationID string) []RotatedFile {
	deduped := dedupRotations(files)

	out := make([]RotatedFile, 0, len(deduped))

	for _, f := range deduped {
		if f.ID > lastRotationID {
			out = append(out, f)
		}
	}

	return out
}

// newestRotationID returns the ID of the newest rotation present, or "" if none.
// Used at bootstrap to set LastRotationID so historical backups are not replayed.
func newestRotationID(files []RotatedFile) string {
	deduped := dedupRotations(files)
	if len(deduped) == 0 {
		return ""
	}

	return deduped[len(deduped)-1].ID
}

// findRotationByID returns the (deduped, .gz-preferred) rotation with the given
// ID from a fresh listing, used to re-resolve a generation after a compression
// race turned its ".log" into ".log.gz".
func findRotationByID(files []RotatedFile, id string) (RotatedFile, bool) {
	for _, f := range dedupRotations(files) {
		if f.ID == id {
			return f, true
		}
	}

	return RotatedFile{}, false
}
