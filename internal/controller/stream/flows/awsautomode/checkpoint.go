// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsautomode

import (
	"crypto/md5" //nolint:gosec // non-cryptographic use: same-file continuation check, no security requirement
	"maps"
	"sync"

	"k8s.io/apimachinery/pkg/types"
)

// nodeLogCheckpoint records how far the collector has consumed a single node's
// Network Policy Agent log. It is used to fetch only new bytes on each poll and
// to detect log rotation, truncation, and node (kubelet) restarts.
//
// Each poll issues an HTTP Range request beginning slightly before ByteOffset (a
// small validation overlap), so only the tail after the checkpoint is streamed. The
// overlap lets the record ending at ByteOffset be re-read and re-hashed to confirm
// the file is the same one before any newer record is emitted. If the server ignores
// the Range (HTTP 200) the whole file is streamed and the already-processed prefix
// is skipped instead; if the offset is past the file (HTTP 416) or the boundary hash
// differs, the file is reprocessed from 0.
type nodeLogCheckpoint struct {
	// NodeName is the Kubernetes node name (used to build the proxy request path).
	NodeName string
	// NodeUID is the node object UID at the time of the last poll. A change in
	// UID means the node object was replaced (node recreated), so any prior
	// offset is meaningless and must be reset.
	NodeUID types.UID
	// ByteOffset is the number of bytes already processed from the current log.
	ByteOffset int
	// LastObservedSize is the total size (in bytes) of the log the last time it
	// was fetched. If a later fetch returns fewer bytes than this, the file was
	// truncated or rotated and the offset must be reset to 0.
	LastObservedSize int
	// LastRecordHash is the MD5 of the last fully-processed line. On the next
	// fetch, if the byte at ByteOffset still hashes to this value the file is the
	// same underlying file and we resume after it; if not, the file was replaced
	// (rotation without shrinking) and we reprocess from 0. It also lets us drop a
	// duplicate final record if a rotation re-emits it.
	LastRecordHash [md5.Size]byte
	// LastRotationID is the newest rotated Lumberjack generation that has been
	// fully processed (its ID is the lumberjack backup timestamp, e.g.
	// "2026-08-07T15-04-05.000"). It is the primary rotation detector: on each
	// poll the collector lists the log directory and recovers any rotated
	// generation whose ID is greater than this. Empty means no rotation has been
	// recovered yet (set at bootstrap to the newest generation already present so
	// historical backups are not replayed). ByteOffset is an UNCOMPRESSED offset
	// into the current active file; when the active file rotates it becomes the
	// oldest unseen generation, and only that first generation is resumed from the
	// stored ByteOffset (later generations are read whole from 0).
	LastRotationID string
}

// hashRecord returns the MD5 of a log line for checkpoint comparison. MD5 is
// used only to detect whether the record at a stored offset is unchanged across
// polls; it carries no security requirement, so the weaker-but-faster hash is
// fine here.
func hashRecord(line []byte) [md5.Size]byte {
	return md5.Sum(line) //nolint:gosec // non-cryptographic use: same-file continuation check
}

// checkpointStore holds per-node checkpoints. It is safe for concurrent use by
// the per-node poll workers.
type checkpointStore struct {
	mu          sync.Mutex
	checkpoints map[string]*nodeLogCheckpoint
}

// newCheckpointStore creates an empty checkpoint store.
func newCheckpointStore() *checkpointStore {
	return &checkpointStore{
		checkpoints: make(map[string]*nodeLogCheckpoint),
	}
}

// get returns the checkpoint for a node, creating a zero-valued one if absent.
func (s *checkpointStore) get(nodeName string) *nodeLogCheckpoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp, ok := s.checkpoints[nodeName]
	if !ok {
		cp = &nodeLogCheckpoint{NodeName: nodeName}
		s.checkpoints[nodeName] = cp
	}

	return cp
}

// set stores an updated checkpoint for a node. Callers must only persist a
// checkpoint after the corresponding bytes have been successfully processed, so
// a failure never silently advances the offset past unprocessed records.
func (s *checkpointStore) set(nodeName string, cp *nodeLogCheckpoint) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.checkpoints[nodeName] = cp
}

// retain drops checkpoints for nodes not present in the given set, returning the
// names removed. Called after each poll cycle so departed nodes don't leak.
func (s *checkpointStore) retain(activeNodes map[string]bool) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var removed []string

	maps.DeleteFunc(s.checkpoints, func(name string, _ *nodeLogCheckpoint) bool {
		if !activeNodes[name] {
			removed = append(removed, name)

			return true
		}

		return false
	})

	return removed
}
