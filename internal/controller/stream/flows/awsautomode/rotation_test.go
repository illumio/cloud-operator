// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsautomode

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const activeBase = "network-policy-agent.log"

func TestParseRotationFilename(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		wantOK   bool
		wantID   string
		wantGzip bool
		wantName string
	}{
		{
			name:     "uncompressed rotation",
			file:     "network-policy-agent-2026-08-07T15-04-05.000.log",
			wantOK:   true,
			wantID:   "2026-08-07T15-04-05.000",
			wantGzip: false,
			wantName: "network-policy-agent-2026-08-07T15-04-05.000.log",
		},
		{
			name:     "compressed rotation",
			file:     "network-policy-agent-2026-08-07T15-04-05.000.log.gz",
			wantOK:   true,
			wantID:   "2026-08-07T15-04-05.000",
			wantGzip: true,
			wantName: "network-policy-agent-2026-08-07T15-04-05.000.log.gz",
		},
		{name: "active file is not a rotation", file: "network-policy-agent.log", wantOK: false},
		{name: "unrelated file", file: "ipamd.log", wantOK: false},
		{name: "unrelated prefix", file: "other-agent-2026-08-07T15-04-05.000.log", wantOK: false},
		{name: "wrong extension", file: "network-policy-agent-2026-08-07T15-04-05.000.txt", wantOK: false},
		// An empty rotation ID is rejected: "network-policy-agent-.log" has the
		// prefix and extension but no timestamp between them.
		{name: "empty id", file: "network-policy-agent-.log", wantOK: false},
		// A non-timestamp suffix is rejected: it must parse as lumberjack's
		// backup timestamp, else a stray file like this ("debug") would sort
		// lexically after real timestamps and stall rotation detection.
		{name: "non-timestamp id", file: "network-policy-agent-debug.log", wantOK: false},
		{name: "malformed timestamp id", file: "network-policy-agent-2026-08-07.log", wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rf, ok := parseRotationFilename(tc.file, activeBase)
			if !tc.wantOK {
				assert.False(t, ok)

				return
			}

			require.True(t, ok)
			assert.Equal(t, tc.wantID, rf.ID)
			assert.Equal(t, tc.wantGzip, rf.Compressed)
			assert.Equal(t, tc.wantName, rf.Filename)
		})
	}
}

func TestDedupRotations_PrefersGzipAndSorts(t *testing.T) {
	in := []RotatedFile{
		{ID: "2026-08-07T12-00-00.000", Filename: "network-policy-agent-2026-08-07T12-00-00.000.log", Compressed: false},
		{ID: "2026-08-07T10-00-00.000", Filename: "network-policy-agent-2026-08-07T10-00-00.000.log.gz", Compressed: true},
		// Same ID as the first, compressed form should win.
		{ID: "2026-08-07T12-00-00.000", Filename: "network-policy-agent-2026-08-07T12-00-00.000.log.gz", Compressed: true},
	}

	out := dedupRotations(in)

	require.Len(t, out, 2)
	// Sorted oldest -> newest.
	assert.Equal(t, "2026-08-07T10-00-00.000", out[0].ID)
	assert.Equal(t, "2026-08-07T12-00-00.000", out[1].ID)
	// Compressed form preferred for the duplicated ID.
	assert.True(t, out[1].Compressed)
}

func TestUnseenRotations(t *testing.T) {
	files := []RotatedFile{
		{ID: "2026-08-07T10-00-00.000", Filename: "a.log.gz", Compressed: true},
		{ID: "2026-08-07T11-00-00.000", Filename: "b.log.gz", Compressed: true},
		{ID: "2026-08-07T12-00-00.000", Filename: "c.log.gz", Compressed: true},
	}

	// Seen through 11:00 -> only 12:00 is unseen.
	unseen := unseenRotations(files, "2026-08-07T11-00-00.000")
	require.Len(t, unseen, 1)
	assert.Equal(t, "2026-08-07T12-00-00.000", unseen[0].ID)

	// Empty lastRotationID -> all unseen, oldest first.
	all := unseenRotations(files, "")
	require.Len(t, all, 3)
	assert.Equal(t, "2026-08-07T10-00-00.000", all[0].ID)
	assert.Equal(t, "2026-08-07T12-00-00.000", all[2].ID)

	// Seen the newest -> none.
	none := unseenRotations(files, "2026-08-07T12-00-00.000")
	assert.Empty(t, none)
}

func TestNewestRotationID(t *testing.T) {
	assert.Empty(t, newestRotationID(nil))

	files := []RotatedFile{
		{ID: "2026-08-07T10-00-00.000", Filename: "a.log.gz", Compressed: true},
		{ID: "2026-08-07T12-00-00.000", Filename: "c.log.gz", Compressed: true},
		{ID: "2026-08-07T11-00-00.000", Filename: "b.log.gz", Compressed: true},
	}
	assert.Equal(t, "2026-08-07T12-00-00.000", newestRotationID(files))
}

func TestFindRotationByID(t *testing.T) {
	files := []RotatedFile{
		{ID: "2026-08-07T10-00-00.000", Filename: "network-policy-agent-2026-08-07T10-00-00.000.log", Compressed: false},
		{ID: "2026-08-07T10-00-00.000", Filename: "network-policy-agent-2026-08-07T10-00-00.000.log.gz", Compressed: true},
	}

	got, ok := findRotationByID(files, "2026-08-07T10-00-00.000")
	require.True(t, ok)
	// Dedup prefers the compressed form.
	assert.True(t, got.Compressed)

	_, ok = findRotationByID(files, "missing")
	assert.False(t, ok)
}
