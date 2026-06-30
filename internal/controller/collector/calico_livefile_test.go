// Copyright 2026 Illumio, Inc. All Rights Reserved.

//go:build livecalico

package collector

import (
	"bufio"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseSyslogFlowAgainstLiveCapture runs every record from a live Felix
// capture through ParseSyslogFlow to confirm the parser handles real production
// output (full decoration metadata, mixed protocols). Gated behind the
// `livecalico` build tag and a capture file path so it never runs in CI.
//
//	go test -tags livecalico -run TestParseSyslogFlowAgainstLiveCapture \
//	  ./internal/controller/collector/ -args /tmp/calico_live_lines.txt
func TestParseSyslogFlowAgainstLiveCapture(t *testing.T) {
	path := os.Getenv("CALICO_CAPTURE_FILE")
	if path == "" {
		path = "/tmp/calico_live_lines.txt"
	}

	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), calicoSyslogMaxLineLength)

	var total, parsed int

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		total++

		flow, err := ParseSyslogFlow(line)
		if err != nil {
			t.Logf("line %d failed: %v\n  %s", total, err, line)

			continue
		}
		parsed++

		require.NotEmpty(t, flow.SourceIP, "line %d", total)
		require.NotEmpty(t, flow.DestinationIP, "line %d", total)
		require.NotEmpty(t, flow.Protocol, "line %d", total)
		require.NotEmpty(t, flow.IPVersion, "line %d", total)
	}

	require.NoError(t, scanner.Err())
	t.Logf("parsed %d/%d live Felix records", parsed, total)
	require.Equal(t, total, parsed, "every live record should parse")
}
