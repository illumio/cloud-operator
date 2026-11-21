// Copyright 2026 Illumio, Inc. All Rights Reserved.

package logging

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// blockedInGoroutineDumpTest blocks until release is closed. The test looks
// for its name in the dump.
func blockedInGoroutineDumpTest(started chan<- struct{}, release <-chan struct{}) {
	close(started)
	<-release
}

func TestLogGoroutineDump(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	go blockedInGoroutineDumpTest(started, release)

	<-started

	defer close(release)

	core, logs := observer.New(zap.ErrorLevel)
	// Add the stack of the caller to errors, as the production logger does.
	logger := zap.New(core, zap.AddStacktrace(zapcore.ErrorLevel))

	var local strings.Builder

	LogGoroutineDump(logger, &local, "resource processing has been running for 5m1s")

	entries := logs.FilterMessage("Goroutine dump").All()
	require.NotEmpty(t, entries)

	logged := make([]string, 0, len(entries))

	for i, entry := range entries {
		fields := entry.ContextMap()
		assert.Equal(t, "resource processing has been running for 5m1s", fields["reason"])
		assert.EqualValues(t, i+1, fields["part"])
		assert.EqualValues(t, len(entries), fields["parts"])
		assert.Empty(t, entry.Stack, "an entry must not carry the stack of the caller")

		chunk, ok := fields["stacks"].(string)
		require.True(t, ok, "the stacks field must be a string")

		logged = append(logged, chunk)
	}

	dump := strings.Join(logged, goroutineSeparator)

	assert.Contains(t, dump, "goroutine ")
	// The arguments of the blocked function are two channels, which the dump
	// would print as addresses.
	assert.Contains(t, dump, "logging.blockedInGoroutineDumpTest(...)\n", "the dump must hold the stack of a blocked goroutine, without its arguments")

	// The local writer gets the whole dump, whatever happens to the log entries.
	assert.Equal(t, "goroutine dump: resource processing has been running for 5m1s\n"+dump+"\n", local.String())
}

func TestRedactGoroutineDumpArguments(t *testing.T) {
	dump := strings.Join([]string{
		"goroutine 67 [select, 4 minutes]:",
		"google.golang.org/grpc/internal/transport.(*writeQuota).get(0xc000123456, 0x40)",
		"\t/go/pkg/mod/google.golang.org/grpc@v1.85.0/internal/transport/flowcontrol.go:60 +0x8a",
		"github.com/illumio/cloud-operator/internal/controller/stream/resources.(*resourcesClient).SendToStream(0xc000aaaaaa, {0xc000bbbbbb, 0x5, 0x5})",
		"\t/workspace/internal/controller/stream/resources/client.go:271 +0x1b4",
		"github.com/illumio/cloud-operator/internal/controller/stream.runStreamWithKeepalive.func1()",
		"\t/workspace/internal/controller/stream/manager.go:128 +0x10c",
		"created by github.com/illumio/cloud-operator/internal/controller/stream.runStreamWithKeepalive in goroutine 67",
		"\t/workspace/internal/controller/stream/manager.go:123 +0x2f5",
	}, "\n")

	expected := strings.Join([]string{
		"goroutine 67 [select, 4 minutes]:",
		"google.golang.org/grpc/internal/transport.(*writeQuota).get(...)",
		"\t/go/pkg/mod/google.golang.org/grpc@v1.85.0/internal/transport/flowcontrol.go:60 +0x8a",
		"github.com/illumio/cloud-operator/internal/controller/stream/resources.(*resourcesClient).SendToStream(...)",
		"\t/workspace/internal/controller/stream/resources/client.go:271 +0x1b4",
		"github.com/illumio/cloud-operator/internal/controller/stream.runStreamWithKeepalive.func1()",
		"\t/workspace/internal/controller/stream/manager.go:128 +0x10c",
		"created by github.com/illumio/cloud-operator/internal/controller/stream.runStreamWithKeepalive in goroutine 67",
		"\t/workspace/internal/controller/stream/manager.go:123 +0x2f5",
	}, "\n")

	assert.Equal(t, expected, redactGoroutineDumpArguments(dump))
}

func TestSplitGoroutineDump(t *testing.T) {
	goroutine := func(id string, lines int) string {
		return "goroutine " + id + " [select]:" + strings.Repeat("\nmain.wait()", lines)
	}

	tests := map[string]struct {
		goroutines     []string
		chunkSize      int
		expectedChunks int
	}{
		"empty dump": {
			goroutines:     nil,
			chunkSize:      100,
			expectedChunks: 0,
		},
		"dump smaller than a chunk": {
			goroutines:     []string{goroutine("1", 1), goroutine("2", 1)},
			chunkSize:      1000,
			expectedChunks: 1,
		},
		"one goroutine per chunk": {
			goroutines:     []string{goroutine("1", 5), goroutine("2", 5), goroutine("3", 5)},
			chunkSize:      100,
			expectedChunks: 3,
		},
		"a stack larger than a chunk is not split": {
			goroutines:     []string{goroutine("1", 1), goroutine("2", 50), goroutine("3", 1)},
			chunkSize:      100,
			expectedChunks: 3,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dump := strings.Join(tt.goroutines, goroutineSeparator)

			chunks := splitGoroutineDump(dump+"\n", tt.chunkSize)

			assert.Len(t, chunks, tt.expectedChunks)
			assert.Equal(t, dump, strings.Join(chunks, goroutineSeparator), "the chunks must hold the whole dump")

			for _, chunk := range chunks {
				assert.True(t, strings.HasPrefix(chunk, "goroutine "), "a chunk must start with a goroutine")
			}
		})
	}
}
