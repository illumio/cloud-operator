// Copyright 2026 Illumio, Inc. All Rights Reserved.

package awsautomode

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5" //nolint:gosec // non-cryptographic: mirrors checkpoint boundary hashing
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/collector"
)

// Benchmark scenario constants: a worst-case 200-node EKS Auto Mode fleet whose
// active Network Policy Agent log is ~180 MiB per node. These exercise the REAL
// restLogFetcher + autoModeClient code paths (node discovery, directory listing,
// ranged/streaming active-file reads, checkpoint continuation) against an
// httptest.Server that generates log bytes lazily, so neither the collector nor the
// mock server ever materializes a 180 MiB buffer.
const (
	benchNumNodes = 1000

	// benchRecordWidth is the fixed byte width of every synthetic flow record
	// (padded JSON line + '\n'). A fixed width makes record boundaries land on
	// exact multiples of the width, so a steady-state checkpoint can be placed on a
	// real boundary and its LastRecordHash computed deterministically.
	benchRecordWidth = 256

	// benchActiveSize is the virtual active-file size: exactly 180 MiB.
	benchActiveSize = int64(180 << 20)

	// benchNewBytes is how much fresh tail sits past the steady-state checkpoint:
	// exactly 1 MiB (a whole number of records).
	benchNewBytes = int64(1 << 20)

	// benchCheckpointOffset is the steady-state ByteOffset: 179 MiB, a record
	// boundary. A poll from here Range-fetches only validationOverlap (1 MiB) +
	// benchNewBytes (1 MiB) = ~2 MiB per node instead of the whole 180 MiB.
	benchCheckpointOffset = benchActiveSize - benchNewBytes

	// benchNewRecords is the number of records emitted per node per steady-state
	// poll (the 1 MiB tail after the checkpoint).
	benchNewRecords = int(benchNewBytes / benchRecordWidth)
)

// benchTile returns a reusable byte tile whose length is a whole multiple of
// benchRecordWidth and whose byte at index i equals record[i % benchRecordWidth],
// where record is a single padded, newline-terminated flow line. Streaming an
// arbitrary virtual byte range [start,end) of the 180 MiB file is then a matter of
// copying slices of this bounded tile at the right phase — the server never holds
// more than the tile in memory. It also returns the single record's bytes WITHOUT
// the trailing newline, whose MD5 is the checkpoint LastRecordHash.
func benchTile() (tile, record []byte) {
	// A recent timestamp so every record passes collector.CacheFlowLine's
	// MaxFlowAge stale-flow filter and is actually cached (exercising the sink).
	ts := time.Now().Add(-time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
	line := `{"level":"info","ts":"` + ts + `","logger":"ebpf-client","msg":"Flow Info: ",` +
		`"Src IP":"10.0.1.1","Src Port":80,"Dest IP":"10.0.1.2","Dest Port":443,` +
		`"Proto":"TCP","Verdict":"ACCEPT"}`

	if len(line) > benchRecordWidth-1 {
		panic(fmt.Sprintf("bench flow line %d bytes exceeds record width %d", len(line), benchRecordWidth-1))
	}

	// Pad with trailing spaces (tolerated by json.Unmarshal) to width-1, then '\n'.
	rec := make([]byte, benchRecordWidth)
	for i := range rec {
		rec[i] = ' '
	}

	copy(rec, line)
	rec[benchRecordWidth-1] = '\n'

	// A ~256 KiB tile (whole records) so the server streams in large chunks.
	const tileRecords = (256 << 10) / benchRecordWidth

	return bytes.Repeat(rec, tileRecords), rec[:benchRecordWidth-1]
}

// countingSink is a minimal collector.FlowSink that counts cached flows without the
// reflection/locking overhead of a testify mock, so the benchmark measures the
// collector rather than the sink.
type countingSink struct {
	cached   atomic.Int64
	received atomic.Int64
}

func (s *countingSink) CacheFlow(_ context.Context, _ pb.Flow) error {
	s.cached.Add(1)

	return nil
}

func (s *countingSink) IncrementFlowsReceived() { s.received.Add(1) }

// rotationSpec advertises one rotated generation the mock directory listing should
// expose, and how the server should answer a fetch for it.
type rotationSpec struct {
	filename   string
	compressed bool
}

// mockNodeProxy is an httptest handler that simulates the Kubernetes apiserver's
// node-list endpoint and the kubelet node-proxy log endpoints for benchNumNodes
// nodes. Log bytes are generated lazily from a bounded tile, so a 180 MiB virtual
// file costs the server only the tile in memory.
type mockNodeProxy struct {
	nodeListJSON []byte
	tile         []byte
	activeSize   int64

	// rangeIgnored, when true, makes the active-file handler ignore the Range header
	// and always answer 200 with the whole virtual file (the OOM worst case).
	rangeIgnored bool

	// rotations, when non-empty, is the set of rotated generations advertised in the
	// directory listing for every node. rotatedSize is the uncompressed size of a
	// ".log" rotation; gzData is the shared gzip body served for a ".gz" rotation.
	rotations   []rotationSpec
	rotatedSize int64
	gzData      []byte

	// Instrumentation.
	logBytes    atomic.Int64 // total log-file bytes streamed (excludes list/dir HTML)
	curProxy    atomic.Int64 // in-flight node-proxy requests right now
	maxProxy    atomic.Int64 // high-water mark of concurrent node-proxy requests
	rotatedGets sync.Map     // rotated filename -> *atomic.Int64 of fetches
}

// rotatedGetCount returns how many times a specific rotated file was fetched.
func (m *mockNodeProxy) rotatedGetCount(filename string) int64 {
	v, ok := m.rotatedGets.Load(filename)
	if !ok {
		return 0
	}

	counter, ok := v.(*atomic.Int64)
	if !ok {
		return 0
	}

	return counter.Load()
}

func (m *mockNodeProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Node discovery is a single non-proxy request per poll cycle; serve it first.
	if path == "/api/v1/nodes" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(m.nodeListJSON)

		return
	}

	// Everything else is a node-proxy request. Track concurrency for the whole
	// lifetime of the request (list, active-file stream, or rotated-file stream).
	// Because a single pollNode issues these sequentially, the peak of this gauge is
	// exactly the number of nodes polled concurrently — what maxConcurrentPolls
	// bounds — regardless of how many HTTP requests each node makes.
	cur := m.curProxy.Add(1)
	defer m.curProxy.Add(-1)

	for {
		hi := m.maxProxy.Load()
		if cur <= hi || m.maxProxy.CompareAndSwap(hi, cur) {
			break
		}
	}

	switch {
	case strings.HasSuffix(path, "/network-policy-agent.log"):
		m.serveActive(w, r)
	case strings.Contains(path, "/network-policy-agent-"):
		m.serveRotated(w, path)
	case strings.HasSuffix(path, "/aws-routed-eni"):
		m.serveDirectory(w)
	default:
		http.NotFound(w, r)
	}
}

// serveDirectory returns kubelet's autoindex HTML: an <a href="name">name</a> per
// entry. The active file is always present; configured rotations are added.
func (m *mockNodeProxy) serveDirectory(w http.ResponseWriter) {
	var b strings.Builder

	b.WriteString("<pre>\n")
	b.WriteString(`<a href="network-policy-agent.log">network-policy-agent.log</a>` + "\n")

	for _, rot := range m.rotations {
		b.WriteString(`<a href="` + rot.filename + `">` + rot.filename + "</a>\n")
	}

	b.WriteString("</pre>\n")

	w.Header().Set("Content-Type", "text/html")
	_, _ = w.Write([]byte(b.String()))
}

// serveActive answers a ranged, streaming GET for the virtual active file. It
// honors a "bytes=<start>-" Range with a 206 + Content-Range (unless rangeIgnored,
// in which case it always streams the whole file with 200).
func (m *mockNodeProxy) serveActive(w http.ResponseWriter, r *http.Request) {
	start := int64(0)

	if !m.rangeIgnored {
		if rng := r.Header.Get("Range"); rng != "" {
			if s, ok := parseBenchRangeStart(rng); ok {
				start = s
			}
		}
	}

	if start >= m.activeSize {
		http.Error(w, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)

		return
	}

	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Length", strconv.FormatInt(m.activeSize-start, 10))

	if start > 0 {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, m.activeSize-1, m.activeSize))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	m.streamRange(w, start, m.activeSize)
}

// serveRotated streams a rotated generation whole (no Range: the checkpoint offset
// is uncompressed and cannot map to a compressed byte range). ".gz" names are
// served from the shared gzip body; ".log" names are streamed lazily from the tile.
func (m *mockNodeProxy) serveRotated(w http.ResponseWriter, path string) {
	// Record the fetch keyed by the rotated file's base name, so tests can assert
	// exactly which generations were opened (e.g. that a ".log.gz" preferred away in
	// favor of its ".log" is never fetched).
	if slash := strings.LastIndexByte(path, '/'); slash >= 0 {
		v, _ := m.rotatedGets.LoadOrStore(path[slash+1:], new(atomic.Int64))
		if counter, ok := v.(*atomic.Int64); ok {
			counter.Add(1)
		}
	}

	if strings.HasSuffix(path, ".gz") {
		w.Header().Set("Content-Length", strconv.Itoa(len(m.gzData)))
		_, _ = w.Write(m.gzData)
		m.logBytes.Add(int64(len(m.gzData)))

		return
	}

	w.Header().Set("Content-Length", strconv.FormatInt(m.rotatedSize, 10))
	m.streamRange(w, 0, m.rotatedSize)
}

// streamRange writes the virtual file's bytes [start,end) from the bounded tile,
// counting them toward logBytes. It never allocates the range.
func (m *mockNodeProxy) streamRange(w http.ResponseWriter, start, end int64) {
	tileLen := int64(len(m.tile))

	pos := start
	for pos < end {
		idx := pos % tileLen
		n := min(end-pos, tileLen-idx)

		written, err := w.Write(m.tile[idx : idx+n])
		m.logBytes.Add(int64(written))

		if err != nil {
			return
		}

		pos += int64(written)
	}
}

// parseBenchRangeStart parses "bytes=<start>-" into start.
func parseBenchRangeStart(h string) (int64, bool) {
	h = strings.TrimSpace(h)
	if !strings.HasPrefix(h, "bytes=") {
		return 0, false
	}

	spec := strings.TrimPrefix(h, "bytes=")

	before, _, found := strings.Cut(spec, "-")
	if !found {
		return 0, false
	}

	start, err := strconv.ParseInt(before, 10, 64)
	if err != nil || start < 0 {
		return 0, false
	}

	return start, true
}

// benchNodeName / benchNodeUID give each node a unique, stable identity.
func benchNodeName(i int) string { return fmt.Sprintf("node-%03d", i) }
func benchNodeUID(i int) string  { return fmt.Sprintf("uid-%03d", i) }

// benchNodeListJSON marshals a NodeList of benchNumNodes Auto Mode nodes, as the
// apiserver would return it, so the real Nodes().List() discovery path decodes it.
func benchNodeListJSON(b *testing.B) []byte {
	b.Helper()

	items := make([]corev1.Node, benchNumNodes)
	for i := range items {
		items[i] = corev1.Node{
			TypeMeta: metav1.TypeMeta{Kind: "Node", APIVersion: "v1"},
			ObjectMeta: metav1.ObjectMeta{
				Name:   benchNodeName(i),
				UID:    types.UID(benchNodeUID(i)),
				Labels: map[string]string{collector.EKSComputeTypeLabel: collector.EKSComputeTypeAuto},
			},
		}
	}

	list := &corev1.NodeList{
		TypeMeta: metav1.TypeMeta{Kind: "NodeList", APIVersion: "v1"},
		Items:    items,
	}

	raw, err := json.Marshal(list)
	if err != nil {
		b.Fatalf("marshal node list: %v", err)
	}

	return raw
}

// benchClientset builds a real kubernetes.Interface whose REST client is backed by
// the httptest server, so restLogFetcher and node discovery both hit the mock.
func benchClientset(b *testing.B, srv *httptest.Server) kubernetes.Interface {
	b.Helper()

	// QPS < 0 disables client-go's client-side rate limiter. Without this the
	// benchmark measures the limiter (200 node LIST + directory-list requests queued
	// behind the default 5 QPS) rather than the collector's streaming path. A real
	// deployment tunes QPS for its fleet size; the raw active/rotated GETs already
	// bypass the limiter by design (see restLogFetcher).
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL, QPS: -1})
	if err != nil {
		b.Fatalf("build clientset: %v", err)
	}

	return cs
}

// seedSteadyStateCheckpoints returns a checkpoint store pre-populated so every node
// is at the steady-state offset (179 MiB) with a valid boundary hash, so the next
// poll validates continuation and Range-fetches only the ~1 MiB tail rather than
// bootstrapping from zero.
func seedSteadyStateCheckpoints(record []byte) *checkpointStore {
	hash := md5.Sum(record) //nolint:gosec // non-cryptographic: matches hashRecord

	store := newCheckpointStore()

	for i := range benchNumNodes {
		name := benchNodeName(i)
		store.set(name, &nodeLogCheckpoint{
			NodeName:         name,
			NodeUID:          types.UID(benchNodeUID(i)),
			ByteOffset:       int(benchCheckpointOffset),
			LastObservedSize: int(benchActiveSize),
			LastRecordHash:   hash,
		})
	}

	return store
}

// newBenchClient wires a real autoModeClient (real fetcher, real checkpoint store,
// real bounded-concurrency poll loop) to the mock server.
func newBenchClient(b *testing.B, srv *httptest.Server, sink collector.FlowSink) *autoModeClient {
	b.Helper()

	cs := benchClientset(b, srv)

	return &autoModeClient{
		logger:             zap.NewNop(),
		flowSink:           sink,
		k8sClient:          cs,
		fetcher:            &restLogFetcher{k8sClient: cs},
		pollInterval:       DefaultPollInterval,
		maxConcurrentPolls: DefaultMaxConcurrentNodePolls,
		checkpoints:        newCheckpointStore(),
	}
}

// heapSampler tracks the peak live heap (HeapInuse) while a benchmark runs. It is
// the direct measure of the OOM fix: even while tens of GiB of active-log bytes
// stream across the wire, the retained heap must stay bounded (a few MiB of tile +
// per-request bufio buffers) rather than growing to maxConcurrentPolls x 180 MiB.
type heapSampler struct {
	peak atomic.Uint64
	stop chan struct{}
	done chan struct{}
}

func startHeapSampler() *heapSampler {
	hs := &heapSampler{stop: make(chan struct{}), done: make(chan struct{})}

	go func() {
		defer close(hs.done)

		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()

		var ms runtime.MemStats

		for {
			select {
			case <-hs.stop:
				return
			case <-ticker.C:
				runtime.ReadMemStats(&ms)

				for {
					cur := hs.peak.Load()
					if ms.HeapInuse <= cur || hs.peak.CompareAndSwap(cur, ms.HeapInuse) {
						break
					}
				}
			}
		}
	}()

	return hs
}

func (hs *heapSampler) stopAndPeak() uint64 {
	close(hs.stop)
	<-hs.done

	return hs.peak.Load()
}

// reportBench attaches the shared custom metrics, including peak live heap.
func reportBench(b *testing.B, mock *mockNodeProxy, peakHeap uint64) {
	b.Helper()

	ops := float64(b.N)
	b.ReportMetric(float64(mock.logBytes.Load())/ops, "log-bytes/op")
	b.ReportMetric(float64(mock.logBytes.Load())/(ops*benchNumNodes), "log-bytes/node")
	b.ReportMetric(float64(mock.maxProxy.Load()), "max-concurrent-nodes")
	b.ReportMetric(float64(peakHeap)/(1<<20), "peak-heap-MiB")
}

// BenchmarkAutoModeCollector200Nodes measures a full steady-state 200-node poll:
// each node's checkpoint sits near the end of its 180 MiB active file, so the
// collector issues an HTTP Range request and streams only the ~2 MiB validation
// overlap + new tail per node — proving memory does not scale as maxConcurrentPolls
// x 180 MiB.
func BenchmarkAutoModeCollector200Nodes(b *testing.B) {
	tile, record := benchTile()

	mock := &mockNodeProxy{
		nodeListJSON: benchNodeListJSON(b),
		tile:         tile,
		activeSize:   benchActiveSize,
	}

	srv := httptest.NewServer(mock)
	defer srv.Close()

	sink := &countingSink{}
	c := newBenchClient(b, srv, sink)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	sampler := startHeapSampler()

	for range b.N {
		// Re-seed so every iteration is an identical steady-state poll (the prior poll
		// advanced offsets to EOF). Excluded from the timer.
		b.StopTimer()

		c.checkpoints = seedSteadyStateCheckpoints(record)

		b.StartTimer()

		c.pollAllNodes(ctx)
	}

	b.StopTimer()
	reportBench(b, mock, sampler.stopAndPeak())
	assertBenchPoll(b, mock, sink, benchNewRecords)
}

// BenchmarkAutoModeCollector200NodesRangeIgnored is the OOM worst case: the server
// ignores the Range header and streams the whole 180 MiB file (200 OK) for every
// node, so ~36 GiB crosses the wire per iteration. The collector must stream and
// skip to the checkpoint tail WITHOUT allocating a 180 MiB buffer per request. Run
// it deliberately (e.g. -benchtime=1x) — it is far heavier than the Range variant.
func BenchmarkAutoModeCollector200NodesRangeIgnored(b *testing.B) {
	tile, record := benchTile()

	mock := &mockNodeProxy{
		nodeListJSON: benchNodeListJSON(b),
		tile:         tile,
		activeSize:   benchActiveSize,
		rangeIgnored: true,
	}

	srv := httptest.NewServer(mock)
	defer srv.Close()

	sink := &countingSink{}
	c := newBenchClient(b, srv, sink)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	sampler := startHeapSampler()

	for range b.N {
		b.StopTimer()

		c.checkpoints = seedSteadyStateCheckpoints(record)

		b.StartTimer()

		c.pollAllNodes(ctx)
	}

	b.StopTimer()

	peak := sampler.stopAndPeak()
	reportBench(b, mock, peak)

	// The headline proof: while 180 MiB/node (~36 GiB total) streamed across the
	// wire, the retained heap stayed bounded — nowhere near maxConcurrentPolls x
	// 180 MiB (~1.8 GiB). Assert it strictly.
	if peakMiB := float64(peak) / (1 << 20); peakMiB > 512 {
		b.Fatalf("peak heap %.0f MiB while streaming whole files; streaming appears broken (expected < 512 MiB)", peakMiB)
	}

	// Each node must have had the whole 180 MiB streamed to it.
	if got := mock.logBytes.Load() / (int64(b.N) * benchNumNodes); got != benchActiveSize {
		b.Fatalf("streamed %d bytes/node, want the whole %d", got, benchActiveSize)
	}

	assertBenchConcurrency(b, mock)

	// Emit count: identical to the Range variant (the checkpoint skips the already-
	// seen prefix even on a 200), so only the transferred bytes differ. This is a
	// 36 GiB loopback stress run, so tolerate a small delivery shortfall from rare
	// connection hiccups — but never silently: log the exact numbers and fail if the
	// shortfall is large enough to indicate a real regression rather than a hiccup.
	want := int64(benchNewRecords) * benchNumNodes * int64(b.N)

	got := sink.cached.Load()
	if got > want {
		b.Fatalf("cached %d flows, want at most %d: the checkpoint prefix-skip regressed and re-emitted old records", got, want)
	}

	if got < want {
		b.Logf("delivery shortfall under 36 GiB stress: cached %d/%d flows (%.2f%%); %d records short",
			got, want, 100*float64(got)/float64(want), want-got)
	}

	if minWant := want - want/10; got < minWant { // tolerate <10% loss to hiccups
		b.Fatalf("cached %d flows, below the %d floor (>10%% short): likely a real regression, not a connection hiccup", got, minWant)
	}
}

// BenchmarkAutoModeCollector200NodesRotation exercises rotation recovery at scale:
// every node's directory lists an older generation present only as ".log.gz"
// (streamed + gunzipped) and a newer generation present as BOTH ".log" and
// ".log.gz" (the corrected behavior: prefer the uncompressed ".log", never open the
// ".gz"). Rotated files are modest so the benchmark stays bounded; the active file
// is tiny here because rotation recovery, not steady-state tailing, is under test.
func BenchmarkAutoModeCollector200NodesRotation(b *testing.B) {
	tile, _ := benchTile()

	const (
		olderGz  = "network-policy-agent-2026-08-07T10-00-00.000.log.gz"
		newerLog = "network-policy-agent-2026-08-07T11-00-00.000.log"
		newerGz  = "network-policy-agent-2026-08-07T11-00-00.000.log.gz"

		rotatedSize = int64(1 << 20) // 1 MiB uncompressed per rotated generation
		activeSize  = int64(64 << 10)
	)

	mock := &mockNodeProxy{
		nodeListJSON: benchNodeListJSON(b),
		tile:         tile,
		activeSize:   activeSize,
		rotations: []rotationSpec{
			{filename: olderGz, compressed: true},
			{filename: newerLog, compressed: false},
			{filename: newerGz, compressed: true},
		},
		rotatedSize: rotatedSize,
		gzData:      benchGzip(tile, rotatedSize),
	}

	srv := httptest.NewServer(mock)
	defer srv.Close()

	sink := &countingSink{}
	c := newBenchClient(b, srv, sink)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	sampler := startHeapSampler()

	for range b.N {
		// Re-seed with an empty LastRotationID so both generations are "unseen" and
		// recovered each iteration; NodeUID is set so this is recovery, not bootstrap.
		b.StopTimer()

		c.checkpoints = seedRotationCheckpoints()

		b.StartTimer()

		c.pollAllNodes(ctx)
	}

	b.StopTimer()
	reportBench(b, mock, sampler.stopAndPeak())

	// Both rotated generations plus the tiny active file contribute flows; assert we
	// actually recovered and cached across all nodes.
	if sink.cached.Load() == 0 {
		b.Fatalf("rotation benchmark cached no flows")
	}

	// Corrected dedup behavior, verified at scale: the newer generation's ".log" is
	// preferred and its co-present ".log.gz" is NEVER fetched. The older generation,
	// present only as ".gz", is streamed and gunzipped.
	wantFetches := int64(benchNumNodes) * int64(b.N)
	if got := mock.rotatedGetCount(newerGz); got != 0 {
		b.Fatalf("newer generation's .log.gz was fetched %d times; the .log should be preferred", got)
	}

	if got := mock.rotatedGetCount(newerLog); got != wantFetches {
		b.Fatalf("newer generation's .log fetched %d times, want %d", got, wantFetches)
	}

	if got := mock.rotatedGetCount(olderGz); got != wantFetches {
		b.Fatalf("older generation's .log.gz fetched %d times, want %d", got, wantFetches)
	}

	assertBenchConcurrency(b, mock)
}

// seedRotationCheckpoints seeds each node with a set NodeUID but empty
// LastRotationID/offset, so the listed rotations are unseen and drive recovery.
func seedRotationCheckpoints() *checkpointStore {
	store := newCheckpointStore()

	for i := range benchNumNodes {
		name := benchNodeName(i)
		store.set(name, &nodeLogCheckpoint{
			NodeName: name,
			NodeUID:  types.UID(benchNodeUID(i)),
		})
	}

	return store
}

// benchGzip returns gzip bytes of `size` uncompressed bytes drawn from the tile.
func benchGzip(tile []byte, size int64) []byte {
	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)

	tileLen := int64(len(tile))
	for pos := int64(0); pos < size; {
		idx := pos % tileLen
		n := min(size-pos, tileLen-idx)

		_, _ = gz.Write(tile[idx : idx+n])
		pos += n
	}

	_ = gz.Close()

	return buf.Bytes()
}

// assertBenchPoll validates a steady-state poll: concurrency stayed within bounds
// and exactly the expected new tail (per node, per iteration) was cached.
func assertBenchPoll(b *testing.B, mock *mockNodeProxy, sink *countingSink, perNode int) {
	b.Helper()

	assertBenchConcurrency(b, mock)

	want := int64(perNode) * benchNumNodes * int64(b.N)
	if got := sink.cached.Load(); got != want {
		b.Fatalf("cached flows = %d, want %d (%d nodes x %d records x %d iters)",
			got, want, benchNumNodes, perNode, b.N)
	}
}

// assertBenchConcurrency checks the collector never exceeded its configured node
// concurrency and that concurrency was actually exercised.
func assertBenchConcurrency(b *testing.B, mock *mockNodeProxy) {
	b.Helper()

	observed := mock.maxProxy.Load()
	if observed > int64(DefaultMaxConcurrentNodePolls) {
		b.Fatalf("observed %d concurrent node-proxy requests, exceeds limit %d",
			observed, DefaultMaxConcurrentNodePolls)
	}

	if observed < 2 {
		b.Fatalf("observed %d concurrent node-proxy requests; expected the poll to run nodes concurrently", observed)
	}
}
