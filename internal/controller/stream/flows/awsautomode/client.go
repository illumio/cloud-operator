// Copyright 2026 Illumio, Inc. All Rights Reserved.

// Package awsautomode collects AWS VPC CNI Network Policy Agent flow logs on EKS
// Auto Mode clusters.
//
// On EKS Auto Mode the VPC CNI and its Network Policy Agent are AWS-managed and
// are NOT exposed as pods, so the standard aws-node pod-log collector cannot see
// them. Instead the agent writes a node-local log file
// (/var/log/aws-routed-eni/network-policy-agent.log) which this collector reads
// through the Kubernetes kubelet node-proxy endpoint:
//
//	GET /api/v1/nodes/{node}/proxy/logs/aws-routed-eni/network-policy-agent.log
//
// This endpoint does not stream, so the collector polls each node on an interval,
// using a per-node checkpoint to fetch only new records and to handle log
// rotation, truncation, and node restarts.
//
// The node-proxy log endpoint is polled rather than streamed. It uses only the
// operator service account, in-cluster API server access, and Kubernetes RBAC
// (nodes + nodes/proxy). It never connects to node IPs directly, mounts host
// paths, runs privileged, or uses AWS credentials/SDKs.
package awsautomode

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/illumio/cloud-operator/internal/controller/collector"
)

// maxRecoveryScanLine bounds a single record length while scanning a decompressed
// rotated file, so a corrupt (missing-newline) backup cannot force an unbounded
// bufio.Scanner buffer allocation. Records far exceed no realistic flow log line.
const maxRecoveryScanLine = 1 << 20 // 1 MiB

// validationOverlap is how many bytes BEFORE the saved ByteOffset the active-file
// Range request starts, so the record ending at ByteOffset is re-read and its hash
// re-validated before any newer record is emitted. It must comfortably exceed the
// longest single flow record so the boundary record always falls fully inside the
// overlap; if it did not, streamActive would be unable to confirm continuation and
// would conservatively reprocess the file from 0.
const validationOverlap = 1 << 20 // 1 MiB

// nodeLogFetcher fetches the Network Policy Agent log for a node through the
// kubelet node-proxy endpoint. Abstracted so tests can back it with httptest.
//
//   - fetchActive issues a ranged, streaming GET for the active log starting at
//     rangeStart and returns an activeStream (206 partial, 200 whole, or 416
//     range-not-satisfiable). It never buffers the whole file.
//   - list returns the rotated generations present in the log directory.
//   - open streams a single rotated file's raw (still-compressed when .gz) bytes,
//     returning errRotatedFileNotFound on 404 (lumberjack compression race).
type nodeLogFetcher interface {
	fetchActive(ctx context.Context, nodeName string, rangeStart int64) (*activeStream, error)
	list(ctx context.Context, nodeName string) ([]RotatedFile, error)
	open(ctx context.Context, nodeName, filename string) (io.ReadCloser, error)
}

// autoModeClient implements the flow collector for EKS Auto Mode nodes.
type autoModeClient struct {
	logger              *zap.Logger
	flowSink            collector.FlowSink
	k8sClient           kubernetes.Interface
	fetcher             nodeLogFetcher
	pollInterval        time.Duration
	maxConcurrentPolls  int
	checkpoints         *checkpointStore
	statsAutoModeNodes  func(int)
	statsAutoModeErrors func()

	// Rotation-recovery stats callbacks (all optional / nil-safe).
	statsRotationsDetected   func(int)
	statsRotationRecovered   func()
	statsRotationRecoveryErr func()
	statsRotationGap         func()
}

// Run polls every EKS Auto Mode node's Network Policy Agent log on an interval
// until the context is cancelled.
func (c *autoModeClient) Run(ctx context.Context) error {
	c.logger.Info("Starting EKS Auto Mode flow collector (node-proxy log polling)",
		zap.Duration("poll_interval", c.pollInterval),
		zap.Int("max_concurrent_node_polls", c.maxConcurrentPolls))

	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()

	// Initial poll immediately so flows start without waiting a full interval.
	c.pollAllNodes(ctx)

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("EKS Auto Mode flow collector stopping")

			return ctx.Err()
		case <-ticker.C:
			c.pollAllNodes(ctx)
		}
	}
}

// pollAllNodes lists Auto Mode nodes and polls each with bounded concurrency.
func (c *autoModeClient) pollAllNodes(ctx context.Context) {
	nodes, err := c.k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: collector.EKSComputeTypeLabel + "=" + collector.EKSComputeTypeAuto,
	})
	if err != nil {
		c.logger.Warn("EKS Auto Mode failed to list nodes", zap.Error(err))

		if c.statsAutoModeErrors != nil {
			c.statsAutoModeErrors()
		}

		return
	}

	if c.statsAutoModeNodes != nil {
		c.statsAutoModeNodes(len(nodes.Items))
	}

	c.logger.Debug("EKS Auto Mode polling nodes", zap.Int("count", len(nodes.Items)))

	sem := make(chan struct{}, c.maxConcurrentPolls)

	var wg sync.WaitGroup

	activeNodes := make(map[string]bool, len(nodes.Items))
	for i := range nodes.Items {
		node := &nodes.Items[i]
		activeNodes[node.Name] = true

		wg.Go(func() {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}

			defer func() { <-sem }()

			nodeLogger := c.logger.With(zap.String("node", node.Name))

			if err := c.pollNode(ctx, node.Name, node.UID, nodeLogger); err != nil {
				nodeLogger.Debug("EKS Auto Mode failed to poll node", zap.Error(err))

				if c.statsAutoModeErrors != nil {
					c.statsAutoModeErrors()
				}
			}
		})
	}

	wg.Wait()

	// Drop checkpoints for nodes that no longer exist.
	for _, removed := range c.checkpoints.retain(activeNodes) {
		c.logger.Debug("EKS Auto Mode removed stale node checkpoint", zap.String("node", removed))
	}
}

// pollNode polls a single node. It runs sequentially for a given node (a node is
// never polled concurrently with itself) and routes to one of three paths:
//
//   - bootstrap: no checkpoint yet, or the node object was replaced (UID change).
//     Record the newest rotation already present WITHOUT replaying history, then
//     read the current active file from the start.
//   - rotation recovery: unseen rotated generations exist (the active file rotated
//     since the last poll). Drain each rotated generation oldest-to-newest, then
//     read the fresh active file.
//   - normal: no unseen rotations; stream the active file from just before the
//     checkpoint and process only newly-appended complete records.
//
// The checkpoint is advanced ONLY after records are successfully processed, so a
// mid-cycle failure never skips unprocessed records; duplicate replay is preferred
// over data loss.
func (c *autoModeClient) pollNode(ctx context.Context, nodeName string, nodeUID types.UID, logger *zap.Logger) error {
	cp := c.checkpoints.get(nodeName)

	// Bootstrap on first sight of a node or when the node object was replaced. A
	// UID change means any prior offset/rotation refers to a different node's file.
	if cp.NodeUID == "" || cp.NodeUID != nodeUID {
		return c.bootstrapNode(ctx, nodeName, nodeUID, logger)
	}

	// List rotations first so we can tell whether the active file rotated since the
	// last poll. A list failure is a poll error; do not advance the checkpoint.
	rotations, err := c.fetcher.list(ctx, nodeName)
	if err != nil {
		return err
	}

	unseen := unseenRotations(rotations, cp.LastRotationID)
	if len(unseen) > 0 {
		logger.Debug("EKS Auto Mode detected rotated generations to recover",
			zap.Int("count", len(unseen)),
			zap.String("last_rotation_id", cp.LastRotationID))

		if c.statsRotationsDetected != nil {
			c.statsRotationsDetected(len(unseen))
		}

		return c.recoverRotations(ctx, nodeName, nodeUID, unseen, logger)
	}

	// Normal path: no unseen rotations. Stream the active file from just before the
	// checkpoint and emit only newly-appended complete records.
	next, err := c.processActive(ctx, nodeName, nodeUID, cp, logger)
	if err != nil {
		return err
	}

	c.checkpoints.set(nodeName, next)

	return nil
}

// bootstrapNode initializes a node's checkpoint without replaying historical
// rotated backups: it records the newest rotation already present as the starting
// LastRotationID, then reads the current active file from the beginning. Called on
// first sight of a node and whenever the node UID changes (node replacement).
func (c *autoModeClient) bootstrapNode(ctx context.Context, nodeName string, nodeUID types.UID, logger *zap.Logger) error {
	rotations, err := c.fetcher.list(ctx, nodeName)
	if err != nil {
		return err
	}

	startRotationID := newestRotationID(rotations)

	logger.Debug("EKS Auto Mode bootstrapping node",
		zap.String("start_rotation_id", startRotationID))

	// Fresh checkpoint at offset 0 for the current active file; do NOT replay the
	// backups that predate startRotationID.
	cp := &nodeLogCheckpoint{
		NodeName:       nodeName,
		NodeUID:        nodeUID,
		LastRotationID: startRotationID,
	}

	next, err := c.processActive(ctx, nodeName, nodeUID, cp, logger)
	if err != nil {
		return err
	}

	next.LastRotationID = startRotationID
	c.checkpoints.set(nodeName, next)

	return nil
}

// recoverRotations drains unseen rotated generations oldest-to-newest, then reads
// the fresh active file. Only the FIRST unseen generation resumes from the stored
// uncompressed ByteOffset (it is the file that just rotated away, of which the
// leading ByteOffset bytes were already processed while it was active); every
// later generation is read whole from 0. Each generation's checkpoint is committed
// after it is fully processed, so a crash mid-recovery does not re-drain already
// recovered generations.
func (c *autoModeClient) recoverRotations(ctx context.Context, nodeName string, nodeUID types.UID, unseen []RotatedFile, logger *zap.Logger) error {
	cp := c.checkpoints.get(nodeName)

	skip := cp.ByteOffset

	for i, rot := range unseen {
		rotLogger := logger.With(zap.String("rotation_id", rot.ID), zap.String("file", rot.Filename))

		// Only the first (oldest) unseen generation is the just-rotated active file,
		// of which the leading ByteOffset bytes were already processed. Later
		// generations are entirely new to us and are read from the start.
		offset := 0
		if i == 0 {
			offset = skip
		}

		recovered, err := c.recoverRotatedFile(ctx, nodeName, rot, offset, rotLogger)
		if err != nil {
			if c.statsRotationRecoveryErr != nil {
				c.statsRotationRecoveryErr()
			}

			return err
		}

		if recovered && c.statsRotationRecovered != nil {
			c.statsRotationRecovered()
		}

		// Commit progress after each generation so recovery is resumable.
		c.checkpoints.set(nodeName, &nodeLogCheckpoint{
			NodeName:       nodeName,
			NodeUID:        nodeUID,
			ByteOffset:     0,
			LastRotationID: rot.ID,
		})
	}

	// After draining rotations, read the fresh active file from the beginning. Its
	// checkpoint was committed at ByteOffset 0 in the loop above.
	activeCP := c.checkpoints.get(nodeName)

	next, err := c.processActive(ctx, nodeName, nodeUID, activeCP, logger)
	if err != nil {
		return err
	}

	c.checkpoints.set(nodeName, next)

	return nil
}

// recoverRotatedFile streams a single rotated generation, skipping the first
// `skip` UNCOMPRESSED bytes (already processed while the file was active), and
// processes the remaining complete records through the shared parse/cache path.
// Handles the lumberjack compression race (a ".log" that 404s after being renamed
// to ".log.gz") by re-listing and re-resolving the same rotation ID.
//
// Returns recovered=true when the generation's bytes were processed, or false when
// it was a recovery gap (the generation was gone before it could be read). A gap is
// not an error: the caller advances past it. Any other failure returns an error.
func (c *autoModeClient) recoverRotatedFile(ctx context.Context, nodeName string, rot RotatedFile, skip int, logger *zap.Logger) (bool, error) {
	rc, err := c.fetcher.open(ctx, nodeName, rot.Filename)
	if errors.Is(err, errRotatedFileNotFound) {
		// Compression race: the ".log" we listed was renamed to ".log.gz" before we
		// fetched it. Re-list, resolve the same rotation ID, and retry once.
		logger.Debug("EKS Auto Mode rotated file vanished (compression race); re-resolving")

		files, listErr := c.fetcher.list(ctx, nodeName)
		if listErr != nil {
			return false, listErr
		}

		resolved, ok := findRotationByID(files, rot.ID)
		if !ok {
			// The generation is genuinely gone (retention deleted it before we read
			// it). Surface a clear recovery gap; the caller advances past it rather
			// than looping. Prefer a visible gap over a silent claim of success.
			logger.Warn("EKS Auto Mode rotation recovery gap: rotated generation no longer present",
				zap.String("rotation_id", rot.ID))

			if c.statsRotationGap != nil {
				c.statsRotationGap()
			}

			return false, nil
		}

		rc, err = c.fetcher.open(ctx, nodeName, resolved.Filename)
		rot = resolved
	}

	if err != nil {
		return false, err
	}

	defer func() { _ = rc.Close() }()

	var reader io.Reader = rc

	if rot.Compressed {
		gz, gzErr := gzip.NewReader(rc)
		if gzErr != nil {
			return false, gzErr
		}
		defer func() { _ = gz.Close() }()

		reader = gz
	}

	// Discard the already-processed prefix from the DECOMPRESSED stream. The
	// checkpoint offset is uncompressed, so this cannot be an HTTP Range on a .gz.
	if skip > 0 {
		if _, err := io.CopyN(io.Discard, reader, int64(skip)); err != nil && !errors.Is(err, io.EOF) {
			return false, err
		}
	}

	if err := c.processReader(ctx, reader, logger); err != nil {
		return false, err
	}

	return true, nil
}

// processReader parses complete newline-delimited records from r and caches any
// flows, using the same parser/cache path as the active-file poll. It is used for
// streaming rotated (optionally gzip-decompressed) files so a ~200MB backup is
// never fully buffered in memory.
func (c *autoModeClient) processReader(ctx context.Context, r io.Reader, logger *zap.Logger) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxRecoveryScanLine)

	flowCount := 0

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}

		b := scanner.Bytes()
		if len(b) == 0 {
			continue
		}

		cached, err := c.processLine(ctx, b, logger)
		if err != nil {
			return err
		}

		if cached {
			flowCount++
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	if flowCount > 0 {
		logger.Debug("EKS Auto Mode recovered flows from rotated file", zap.Int("flow_count", flowCount))
	}

	return nil
}

// processActive streams the active log for a node and returns the checkpoint to
// persist AFTER the emitted records have been cached. It issues a Range request
// starting validationOverlap bytes before the saved ByteOffset so the boundary
// record is re-read and its hash re-validated before any newer record is emitted;
// only the small tail after the checkpoint crosses the wire on a steady-state poll.
//
// When the server ignores the Range (HTTP 200) the whole file is streamed instead,
// and streamActive still skips the already-processed prefix. When the file no
// longer contains the checkpoint (HTTP 416, or a boundary hash mismatch — the file
// was truncated, shrank, or was replaced) the active file is reprocessed from
// offset 0. Because streamActive emits nothing until the continuation is validated,
// a reset never double-sends records.
func (c *autoModeClient) processActive(ctx context.Context, nodeName string, nodeUID types.UID, cp *nodeLogCheckpoint, logger *zap.Logger) (*nodeLogCheckpoint, error) {
	rangeStart := int64(0)
	if cp.ByteOffset > validationOverlap {
		rangeStart = int64(cp.ByteOffset - validationOverlap)
	}

	res, err := c.readActiveOnce(ctx, nodeName, cp, rangeStart, logger)
	if err != nil {
		return nil, err
	}

	if res.needReset {
		logger.Debug("EKS Auto Mode detected log reset (truncation/replacement); reprocessing active file from start")

		// Reprocess the whole file from offset 0 against a fresh checkpoint. The
		// prior LastRotationID is preserved: a reset is a same-generation shrink, not
		// a rotation (rotations are handled before ever reaching processActive).
		fresh := &nodeLogCheckpoint{
			NodeName:       nodeName,
			NodeUID:        nodeUID,
			LastRotationID: cp.LastRotationID,
		}

		res, err = c.readActiveOnce(ctx, nodeName, fresh, 0, logger)
		if err != nil {
			return nil, err
		}

		cp = fresh
	}

	next := *cp
	next.NodeUID = nodeUID
	next.ByteOffset = res.newOffset
	next.LastObservedSize = res.observedSize
	next.LastRecordHash = res.lastHash

	return &next, nil
}

// readActiveOnce performs one streaming active-log fetch at rangeStart and pipes
// each complete record through the shared parse/cache path, never buffering the
// whole file. A 416 (range beyond the current file) is reported as needReset so the
// caller reprocesses from 0. The response body is always closed.
func (c *autoModeClient) readActiveOnce(ctx context.Context, nodeName string, cp *nodeLogCheckpoint, rangeStart int64, logger *zap.Logger) (streamActiveResult, error) {
	stream, err := c.fetcher.fetchActive(ctx, nodeName, rangeStart)
	if err != nil {
		return streamActiveResult{}, err
	}

	// 416: the requested range is past the current active file (it shrank below the
	// checkpoint). No body to close. Signal a reset so the caller re-reads from 0.
	if stream.statusCode == http.StatusRequestedRangeNotSatisfiable {
		return streamActiveResult{needReset: true}, nil
	}

	defer func() { _ = stream.Body.Close() }()

	flowCount := 0

	emit := func(rec []byte) error {
		cached, e := c.processLine(ctx, rec, logger)
		if e != nil {
			return e
		}

		if cached {
			flowCount++
		}

		return nil
	}

	res, err := streamActive(int(stream.bodyStart), stream.Body, cp, emit)
	if err != nil {
		return res, err
	}

	if flowCount > 0 {
		logger.Debug("EKS Auto Mode parsed flows from node", zap.Int("flow_count", flowCount))
	}

	return res, nil
}

// processLine parses a single log line and, if it is a flow record within the
// stream time window, caches it. Delegates to collector.CacheFlowLine, the shared
// parse -> stale-filter -> cache path used by both AWS VPC CNI collectors, passing
// a rolling notBefore so flows older than the window are dropped rather than sent
// (on restart the whole active log is re-read, so the tail can be far in the past).
//
// Returns cached=true when a flow was cached. A parse failure is not an error
// (non-flow housekeeping lines are common); only a context error is propagated so
// the caller can stop without advancing the checkpoint.
func (c *autoModeClient) processLine(ctx context.Context, line []byte, logger *zap.Logger) (bool, error) {
	notBefore := time.Now().Add(-collector.MaxFlowAge)

	return collector.CacheFlowLine(ctx, c.flowSink, string(line), notBefore, logger)
}
