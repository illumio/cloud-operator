// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsautomode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/illumio/cloud-operator/internal/controller/collector"
)

// errRotatedFileNotFound is returned by open when a rotated file named in a prior
// directory listing is no longer fetchable (HTTP 404). This is expected during
// lumberjack's compression race: a generation briefly exists as ".log" and then
// is renamed to ".log.gz". Callers re-list and retry with the resolved name
// rather than treating it as data loss.
var errRotatedFileNotFound = errors.New("rotated log file not found")

// errNoHTTPClient is returned when the REST client does not expose an underlying
// *http.Client, so the fetcher cannot issue the raw ranged/streaming request it
// needs. This should not happen with a real in-cluster clientset.
var errNoHTTPClient = errors.New("REST client does not expose an *http.Client for streaming")

// hrefPattern extracts entries from the kubelet's directory autoindex HTML, whose
// entries are rendered as <a href="name">name</a>.
var hrefPattern = regexp.MustCompile(`href="([^"?]+)"`)

// restLogFetcher fetches a node's Network Policy Agent log through the kubelet
// node-proxy endpoint. It builds:
//
//	GET /api/v1/nodes/{node}/proxy/logs/aws-routed-eni/network-policy-agent.log
//
// The active log is fetched with an HTTP Range request and STREAMED, never read
// whole into memory: the active file can approach ~200MB (lumberjack's default
// rotation size) and buffering it on every poll, across concurrent node polls,
// caused large memory spikes and OOM kills. Because we keep a per-node ByteOffset,
// normal polling only needs the small tail after the checkpoint. Rotated files are
// likewise streamed (and gunzipped on the fly) rather than buffered.
//
// There is no typed client-go helper for nodes/proxy log files, and neither
// req.Stream() (which hides the HTTP status, so 206-vs-200 is indistinguishable)
// nor req.Do().Raw() (which buffers the whole body) fits. The fetcher therefore
// issues the GET through the REST client's underlying *http.Client: this reuses
// the operator's in-cluster credentials, TLS, and transport (no node IP, host
// mount, or AWS SDK), while exposing resp.StatusCode and resp.Body for streaming.
// Note it bypasses client-go's per-client rate limiter, which is intentional for
// this bounded, low-frequency node-proxy poll.
//
// Rotation note: the AWS VPC CNI Network Policy Agent writes this file via
// lumberjack, which rotates by SIZE. When the active file reaches its max size it
// is RENAMED to a timestamped backup (network-policy-agent-<ts>.log) and then
// compressed in place to network-policy-agent-<ts>.log.gz, creating a fresh, empty
// network-policy-agent.log at the SAME path. The active file is always polled at
// the stable path; rotated generations are discovered via list() and recovered via
// open() so records written just before a rotation are not lost.
type restLogFetcher struct {
	k8sClient kubernetes.Interface
	logPath   string
	// logger is optional (nil-safe); when set, the constructed node-proxy request
	// URL is logged at Debug before the request is issued.
	logger *zap.Logger
}

// activeStream is the result of a ranged, streaming active-log fetch. The caller
// MUST Close Body (it is nil for a 416, which carries no body).
type activeStream struct {
	// statusCode is the raw HTTP status: 206 (Range honored), 200 (Range ignored,
	// whole file streamed), or 416 (offset beyond current file: rotation/truncation).
	statusCode int
	// bodyStart is the absolute byte offset in the active file at which Body begins:
	// the Content-Range start for a 206, or 0 for a 200. Meaningless for a 416.
	bodyStart int64
	// Body streams the (partial) active file. nil for a 416.
	Body io.ReadCloser
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

// httpClient returns the REST client's underlying *http.Client, which carries the
// operator's in-cluster auth and TLS. It is used for the raw ranged/streaming GETs
// that the typed helpers cannot express.
func (f *restLogFetcher) httpClient() (*http.Client, error) {
	rc, ok := f.k8sClient.CoreV1().RESTClient().(*rest.RESTClient)
	if !ok || rc.Client == nil {
		return nil, errNoHTTPClient
	}

	return rc.Client, nil
}

// requestURL builds the absolute node-proxy URL for the given suffix segments,
// reusing the REST request builder so escaping and the API path prefix match the
// rest of the client.
func (f *restLogFetcher) requestURL(nodeName string, segments []string) string {
	return f.k8sClient.CoreV1().RESTClient().
		Get().
		Resource("nodes").
		Name(nodeName).
		SubResource("proxy").
		Suffix(segments...).
		URL().String()
}

// rawGet issues a streaming GET for the given node-proxy segments. When
// rangeStart > 0 it sets a "bytes=<rangeStart>-" Range header. It returns the raw
// *http.Response so callers can branch on StatusCode and stream Body; callers MUST
// Close Body on any non-error, non-416 response.
func (f *restLogFetcher) rawGet(ctx context.Context, nodeName string, segments []string, rangeStart int64) (*http.Response, error) {
	client, err := f.httpClient()
	if err != nil {
		return nil, err
	}

	url := f.requestURL(nodeName, segments)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	if rangeStart > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", rangeStart))
	}

	if f.logger != nil {
		f.logger.Debug("EKS Auto Mode node-proxy GET",
			zap.String("node", nodeName),
			zap.String("url", url),
			zap.Int64("range_start", rangeStart))
	}

	return client.Do(req)
}

// fetchActive issues a ranged, streaming GET for the active log starting at
// rangeStart and returns an activeStream describing how the server answered.
//
//   - 206 Partial Content: Range honored; Body streams from bodyStart (the
//     Content-Range start).
//   - 200 OK: Range ignored; Body streams the whole file from offset 0. Still
//     streamed so memory stays bounded even though the whole file crosses the wire.
//   - 416 Range Not Satisfiable: the offset is beyond the current active
//     generation (rotation/truncation). Body is nil; the caller re-lists rotations
//     and enters recovery rather than assuming "no new data".
//
// Any other status is surfaced as an error, with a bounded snippet of the body
// logged for debugging.
func (f *restLogFetcher) fetchActive(ctx context.Context, nodeName string, rangeStart int64) (*activeStream, error) {
	resp, err := f.rawGet(ctx, nodeName, f.activeSegments(), rangeStart)
	if err != nil {
		return nil, err
	}

	switch resp.StatusCode {
	case http.StatusPartialContent:
		start := parseContentRangeStart(resp.Header.Get("Content-Range"), rangeStart)

		return &activeStream{statusCode: resp.StatusCode, bodyStart: start, Body: resp.Body}, nil
	case http.StatusOK:
		return &activeStream{statusCode: resp.StatusCode, bodyStart: 0, Body: resp.Body}, nil
	case http.StatusRequestedRangeNotSatisfiable:
		_ = resp.Body.Close()

		return &activeStream{statusCode: resp.StatusCode}, nil
	default:
		return nil, f.statusError(resp, nodeName)
	}
}

// open streams a single rotated file's raw bytes for a node. The returned reader
// yields the file exactly as stored (still gzip-compressed when it is a ".gz");
// callers wrap it with gzip.NewReader as needed and stream it, so a ~200MB
// decompressed generation is never fully materialized. Returns
// errRotatedFileNotFound on HTTP 404 so callers can handle the lumberjack
// compression race.
func (f *restLogFetcher) open(ctx context.Context, nodeName, filename string) (io.ReadCloser, error) {
	segs := append(f.dirSegments(), filename)

	resp, err := f.rawGet(ctx, nodeName, segs, 0)
	if err != nil {
		return nil, err
	}

	switch resp.StatusCode {
	case http.StatusOK:
		return resp.Body, nil
	case http.StatusNotFound:
		_ = resp.Body.Close()

		return nil, errRotatedFileNotFound
	default:
		return nil, fmt.Errorf("opening rotated log %q failed: %w", filename, f.statusError(resp, nodeName))
	}
}

// list returns the rotated generations of the Network Policy Agent log present in
// the log directory, parsed from the kubelet directory autoindex. The listing is
// small HTML, so it is read whole rather than streamed. The active file itself is
// excluded (it has no rotation timestamp). Results are not deduped or sorted here;
// callers use dedupRotations/unseenRotations.
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

// statusError reads a bounded snippet of an error response body (the kubelet
// node-proxy returns plain-text bodies that client-go would otherwise hide),
// closes the body, and returns a descriptive error.
func (f *restLogFetcher) statusError(resp *http.Response, nodeName string) error {
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyLog))
	_ = resp.Body.Close()

	if f.logger != nil {
		f.logger.Debug("EKS Auto Mode node-proxy GET failed",
			zap.String("node", nodeName),
			zap.Int("http_status", resp.StatusCode),
			zap.ByteString("body", snippet))
	}

	return fmt.Errorf("node-proxy GET for %q returned HTTP %d: %s", nodeName, resp.StatusCode, strings.TrimSpace(string(snippet)))
}

// maxErrorBodyLog caps how many bytes of an error response body are read for
// logging, so a large error page (or log file) does not flood memory or logs.
const maxErrorBodyLog = 512

// parseContentRangeStart extracts the start offset from a Content-Range header of
// the form "bytes 175000-179999/180000". It falls back to the requested
// rangeStart when the header is missing or malformed, which is safe because a
// well-behaved 206 always echoes the range it served.
func parseContentRangeStart(header string, fallback int64) int64 {
	// Expected: "bytes <start>-<end>/<total>".
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(header, "bytes ") {
		return fallback
	}

	spec := strings.TrimPrefix(header, "bytes ")

	dash := strings.IndexByte(spec, '-')
	if dash <= 0 {
		return fallback
	}

	start, err := strconv.ParseInt(spec[:dash], 10, 64)
	if err != nil || start < 0 {
		return fallback
	}

	return start
}
