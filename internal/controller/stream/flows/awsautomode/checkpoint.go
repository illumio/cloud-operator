// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsautomode

import (
	"crypto/sha256"
	"sync"

	"k8s.io/apimachinery/pkg/types"
)

// nodeLogCheckpoint records how far the collector has consumed a single node's
// Network Policy Agent log. It is used to fetch only new bytes on each poll and
// to detect log rotation, truncation, and node (kubelet) restarts.
//
// The kubelet node-proxy log endpoint returns the whole file on each request and
// does not support range reads, so "incremental" consumption is emulated: we
// fetch the file, then skip the bytes we have already processed (ByteOffset).
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
	// LastRecordHash is the SHA-256 of the last fully-processed line. On the next
	// fetch, if the byte at ByteOffset still hashes to this value the file is the
	// same underlying file and we resume after it; if not, the file was replaced
	// (rotation without shrinking) and we reprocess from 0. It also lets us drop a
	// duplicate final record if a rotation re-emits it.
	LastRecordHash [sha256.Size]byte
}

// hashRecord returns the SHA-256 of a log line for checkpoint comparison.
func hashRecord(line []byte) [sha256.Size]byte {
	return sha256.Sum256(line)
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

	for name := range s.checkpoints {
		if !activeNodes[name] {
			delete(s.checkpoints, name)
			removed = append(removed, name)
		}
	}

	return removed
}
