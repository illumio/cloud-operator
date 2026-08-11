// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsautomode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"

	"github.com/illumio/cloud-operator/internal/controller/collector"
)

// errRotatedFileNotFound is returned by open when a rotated file named in a prior
// directory listing is no longer fetchable (HTTP 404). This is expected during
// lumberjack's compression race: a generation briefly exists as ".log" and then
// is renamed to ".log.gz". Callers re-list and retry with the resolved name
// rather than treating it as data loss.
var errRotatedFileNotFound = errors.New("rotated log file not found")

// hrefPattern extracts entries from the kubelet's directory autoindex HTML, whose
// entries are rendered as <a href="name">name</a>.
var hrefPattern = regexp.MustCompile(`href="([^"?]+)"`)

// restLogFetcher fetches a node's Network Policy Agent log through the kubelet
// node-proxy endpoint using the shared clientset's REST client. It builds:
//
//	GET /api/v1/nodes/{node}/proxy/logs/aws-routed-eni/network-policy-agent.log
//
// There is no typed client-go helper for nodes/proxy log files, so this uses the
// raw REST request builder. It reuses the operator's in-cluster credentials,
// rate limiter, and transport — no node IP, host mount, or AWS SDK involved.
//
// Rotation note: the AWS VPC CNI Network Policy Agent writes this file via
// lumberjack, which rotates by SIZE rather than by adding a new numbered file we
// would have to discover. When the active file reaches its max size (200MB by
// default) lumberjack RENAMES the current network-policy-agent.log to a
// timestamped backup (network-policy-agent-<ts>.log) and then compresses it in
// place to network-policy-agent-<ts>.log.gz, creating a fresh, empty
// network-policy-agent.log at the SAME path. The active file is always polled at
// the stable path; rotated generations are discovered via list() and recovered
// via open() so records written just before a rotation are not lost.
type restLogFetcher struct {
	k8sClient kubernetes.Interface
	logPath   string
	// logger is optional (nil-safe); when set, the constructed node-proxy request
	// URL is logged at Debug before the request is issued.
	logger *zap.Logger
}

// activeSegments returns the node-proxy path segments for the active log file,
// e.g. ["logs","aws-routed-eni","network-policy-agent.log"].
func (f *restLogFetcher) activeSegments() []string {
	return collector.NetworkPolicyAgentLogPathSegments(f.logPath)
}

// dirSegments returns the node-proxy path segments for the directory containing
// the active log, e.g. ["logs","aws-routed-eni"].
func (f *restLogFetcher) dirSegments() []string {
	segs := f.activeSegments()
	if len(segs) <= 1 {
		return segs
	}

	return segs[:len(segs)-1]
}

// activeBaseName returns the active log's base file name, e.g.
// "network-policy-agent.log".
func (f *restLogFetcher) activeBaseName() string {
	segs := f.activeSegments()

	return segs[len(segs)-1]
}

// fetch retrieves the full active log file bytes for a node.
func (f *restLogFetcher) fetch(ctx context.Context, nodeName string) ([]byte, error) {
	req := f.k8sClient.CoreV1().RESTClient().
		Get().
		Resource("nodes").
		Name(nodeName).
		SubResource("proxy").
		Suffix(f.activeSegments()...)

	if f.logger != nil {
		f.logger.Debug("EKS Auto Mode fetching node-proxy log",
			zap.String("node", nodeName),
			zap.String("url", req.URL().String()))
	}

	// Capture the HTTP status code and raw response so failures are debuggable.
	// The kubelet node-proxy returns plain-text (non-Status) error bodies, which
	// client-go surfaces only as "unknown (get nodes <name>)"; the real cause
	// (403 authz text, 404 file-not-found, etc.) lives in the status code and body.
	result := req.Do(ctx)

	statusCode := 0
	result.StatusCode(&statusCode)

	raw, err := result.Raw()
	if err != nil {
		if f.logger != nil {
			f.logger.Debug("EKS Auto Mode node-proxy fetch failed",
				zap.String("node", nodeName),
				zap.Int("http_status", statusCode),
				zap.Int("body_bytes", len(raw)),
				zap.ByteString("body", truncateBody(raw)),
				zap.Error(err))
		}

		return nil, err
	}

	if f.logger != nil {
		f.logger.Debug("EKS Auto Mode node-proxy fetch succeeded",
			zap.String("node", nodeName),
			zap.Int("http_status", statusCode),
			zap.Int("body_bytes", len(raw)))
	}

	return raw, nil
}

// list returns the rotated generations of the Network Policy Agent log present in
// the log directory, parsed from the kubelet directory autoindex. The active file
// itself is excluded (it has no rotation timestamp). Results are not deduped or
// sorted here; callers use dedupRotations/unseenRotations.
func (f *restLogFetcher) list(ctx context.Context, nodeName string) ([]RotatedFile, error) {
	req := f.k8sClient.CoreV1().RESTClient().
		Get().
		Resource("nodes").
		Name(nodeName).
		SubResource("proxy").
		Suffix(f.dirSegments()...)

	if f.logger != nil {
		f.logger.Debug("EKS Auto Mode listing node-proxy log directory",
			zap.String("node", nodeName),
			zap.String("url", req.URL().String()))
	}

	raw, err := req.Do(ctx).Raw()
	if err != nil {
		return nil, fmt.Errorf("listing log directory: %w", err)
	}

	base := f.activeBaseName()

	var files []RotatedFile

	for _, m := range hrefPattern.FindAllSubmatch(raw, -1) {
		name := string(m[1])
		if rf, ok := parseRotationFilename(name, base); ok {
			files = append(files, rf)
		}
	}

	return files, nil
}

// open streams a single rotated file's raw bytes for a node. The returned reader
// yields the file exactly as stored (still gzip-compressed when file.Compressed);
// callers wrap it with gzip.NewReader as needed. Returns errRotatedFileNotFound on
// HTTP 404 so callers can handle the lumberjack compression race.
//
// The compressed bytes are read into memory here (they are far smaller than the
// decompressed log), but decompression by the caller streams from this buffer, so
// the ~200MB decompressed content is never fully materialized.
func (f *restLogFetcher) open(ctx context.Context, nodeName, filename string) (io.ReadCloser, error) {
	segs := append(f.dirSegments(), filename)

	req := f.k8sClient.CoreV1().RESTClient().
		Get().
		Resource("nodes").
		Name(nodeName).
		SubResource("proxy").
		Suffix(segs...)

	if f.logger != nil {
		f.logger.Debug("EKS Auto Mode opening rotated node-proxy log",
			zap.String("node", nodeName),
			zap.String("file", filename),
			zap.String("url", req.URL().String()))
	}

	result := req.Do(ctx)

	statusCode := 0
	result.StatusCode(&statusCode)

	raw, err := result.Raw()
	if err != nil {
		if statusCode == 404 {
			return nil, errRotatedFileNotFound
		}

		return nil, fmt.Errorf("opening rotated log %q: %w", filename, err)
	}

	return io.NopCloser(bytes.NewReader(raw)), nil
}

// rangeResult is the outcome of a Range request against the active file.
type rangeResult struct {
	// statusCode is the HTTP status (206 partial, 200 full, 416 unsatisfiable).
	statusCode int
	// data is the response body.
	data []byte
	// partial is true only when the server honored the Range (HTTP 206).
	partial bool
}

// fetchRange requests the active file starting at offset via an HTTP Range header.
// It is a transport capability for a future incremental-read optimization; the
// poll path deliberately does NOT use it, because reconcile's shrink/hash
// reset-detection needs the whole file. Callers must treat 200 as "Range ignored,
// full body returned" and fall back to whole-file reconcile, and 416 as "offset
// past end" (no new data). Range is never used for gzip recovery, where the
// uncompressed checkpoint offset does not map to a compressed byte position.
func (f *restLogFetcher) fetchRange(ctx context.Context, nodeName string, offset int) (rangeResult, error) {
	req := f.k8sClient.CoreV1().RESTClient().
		Get().
		Resource("nodes").
		Name(nodeName).
		SubResource("proxy").
		Suffix(f.activeSegments()...).
		SetHeader("Range", "bytes="+strconv.Itoa(offset)+"-")

	result := req.Do(ctx)

	statusCode := 0
	result.StatusCode(&statusCode)

	raw, err := result.Raw()

	// A 416 (Range Not Satisfiable) is not an application error for us: it means
	// the offset is at/after the end, i.e. no new bytes. Surface it as a result.
	if statusCode == 416 {
		return rangeResult{statusCode: statusCode, data: nil, partial: false}, nil
	}

	if err != nil {
		return rangeResult{statusCode: statusCode}, err
	}

	return rangeResult{
		statusCode: statusCode,
		data:       raw,
		partial:    statusCode == 206,
	}, nil
}

// truncateBody caps a response body so a large error page (or log file) does not
// flood the debug logs. Only the first 512 bytes are kept.
func truncateBody(b []byte) []byte {
	const maxBodyLog = 512
	if len(b) > maxBodyLog {
		return b[:maxBodyLog]
	}

	return b
}
