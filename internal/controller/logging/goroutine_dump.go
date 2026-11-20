// Copyright 2026 Illumio, Inc. All Rights Reserved.

package logging

import (
	"bytes"
	"runtime/pprof"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// goroutineDumpChunkSize is the size above which the goroutine stacks are
// split into another log entry. A dump of a hundred goroutines is hundreds of
// kilobytes, which is too large for one log entry.
const goroutineDumpChunkSize = 16 * 1024

// goroutineSeparator separates the stacks of two goroutines in a dump.
const goroutineSeparator = "\n\n"

// LogGoroutineDump logs the stack of every goroutine, in several log entries.
//
// A stalled operator does not crash, so the Go runtime prints no stacks, and
// the logs show only that progress stopped. The stacks show where each
// goroutine waits. They contain function names, file names and line numbers,
// and no data.
func LogGoroutineDump(logger *zap.Logger, reason string) {
	var dump bytes.Buffer

	// Debug level 2 prints the stacks in the format of an unrecovered panic,
	// with the state of each goroutine and how long it has waited.
	if err := pprof.Lookup("goroutine").WriteTo(&dump, 2); err != nil {
		logger.Error("Cannot capture goroutine dump", zap.Error(err))

		return
	}

	// The logger adds the stack of the caller to every error. It is of no use
	// in entries that hold the stacks of all the goroutines.
	logger = logger.WithOptions(zap.AddStacktrace(zapcore.InvalidLevel))

	chunks := splitGoroutineDump(dump.String(), goroutineDumpChunkSize)
	for i, chunk := range chunks {
		logger.Error("Goroutine dump",
			zap.String("reason", reason),
			zap.Int("part", i+1),
			zap.Int("parts", len(chunks)),
			zap.String("stacks", chunk),
		)
	}
}

// splitGoroutineDump splits a goroutine dump into chunks of about chunkSize
// bytes. It never splits the stack of one goroutine, so a chunk is larger than
// chunkSize if one stack is.
func splitGoroutineDump(dump string, chunkSize int) []string {
	var (
		chunks  []string
		current strings.Builder
	)

	for _, goroutine := range strings.Split(strings.TrimSpace(dump), goroutineSeparator) {
		if current.Len() > 0 && current.Len()+len(goroutineSeparator)+len(goroutine) > chunkSize {
			chunks = append(chunks, current.String())
			current.Reset()
		}

		if current.Len() > 0 {
			current.WriteString(goroutineSeparator)
		}

		current.WriteString(goroutine)
	}

	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}

	return chunks
}
