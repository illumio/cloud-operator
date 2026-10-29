// Copyright 2026 Illumio, Inc. All Rights Reserved.

//go:build livecalico

package calicoenterprise

import (
	"bufio"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseFlowAgainstLiveCapture runs every record from a live LogCollector
// capture through ParseFlow to confirm it handles real production
// output (full decoration metadata, mixed protocols). Gated behind the
// `livecalico` build tag and a capture file path so it never runs in CI.
//
//	go test -tags livecalico -run TestParseFlowAgainstLiveCapture \
//	  ./internal/controller/collector/ -args /tmp/calico_live_lines.txt
func TestParseFlowAgainstLiveCapture(t *testing.T) {
	path := os.Getenv("CALICO_CAPTURE_FILE")
	if path == "" {
		path = "/tmp/calico_live_lines.txt"
	}

	f, err := os.Open(path) //nolint:gosec // the capture path is chosen by the developer running the test
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), calicoSyslogMaxLineLength)

	var total, parsed int

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		total++

		flow, err := ParseFlow(line)
		if err != nil {
			t.Logf("line %d failed: %v\n  %s", total, err, line)

			continue
		}

		parsed++

		require.NotNil(t, flow.Key(), "line %d", total)
	}

	require.NoError(t, scanner.Err())
	t.Logf("parsed %d/%d live Calico records", parsed, total)
	require.Equal(t, total, parsed, "every live record should parse")
}
