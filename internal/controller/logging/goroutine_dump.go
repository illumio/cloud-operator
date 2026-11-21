// Copyright 2026 Illumio, Inc. All Rights Reserved.

package logging

import (
	"bytes"
	"fmt"
	"io"
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

// LogGoroutineDump writes the stack of every goroutine to local, and then logs
// it in several log entries.
//
// A stalled operator does not crash, so the Go runtime prints no stacks, and
// the logs show only that progress stopped. The stacks show where each
// goroutine waits.
//
// The dump holds the state of each goroutine, function names, file names and
// line numbers. The arguments of the functions are removed: they are raw
// values and addresses from the memory of the process.
//
// The whole dump is written to local first, because the logger also sends each
// entry to CloudSecure and can block on a log stream that the peer stopped
// reading. Call LogGoroutineDump in its own goroutine if the caller must not
// block.
func LogGoroutineDump(logger *zap.Logger, local io.Writer, reason string) {
	var profile bytes.Buffer

	// Debug level 2 prints the stacks in the format of an unrecovered panic,
	// with the state of each goroutine and how long it has waited.
	if err := pprof.Lookup("goroutine").WriteTo(&profile, 2); err != nil {
		logger.Error("Cannot capture goroutine dump", zap.Error(err))

		return
	}

	dump := redactGoroutineDumpArguments(strings.TrimSpace(profile.String()))

	_, _ = fmt.Fprintf(local, "goroutine dump: %s\n%s\n", reason, dump)

	// The logger adds the stack of the caller to every error. It is of no use
	// in entries that hold the stacks of all the goroutines.
	logger = logger.WithOptions(zap.AddStacktrace(zapcore.InvalidLevel))

	chunks := splitGoroutineDump(dump, goroutineDumpChunkSize)
	for i, chunk := range chunks {
		logger.Error("Goroutine dump",
			zap.String("reason", reason),
			zap.Int("part", i+1),
			zap.Int("parts", len(chunks)),
			zap.String("stacks", chunk),
		)
	}
}

// redactGoroutineDumpArguments replaces the arguments of every function call in
// a goroutine dump with "...". A call is a line such as
// "main.(*server).run(0xc000012345, {0x1, 0x2})", followed by an indented line
// with its file and line number.
func redactGoroutineDumpArguments(dump string) string {
	lines := strings.Split(dump, "\n")

	for i, line := range lines {
		if !strings.HasSuffix(line, ")") ||
			strings.HasPrefix(line, "\t") ||
			strings.HasPrefix(line, "goroutine ") ||
			strings.HasPrefix(line, "created by ") {
			continue
		}

		// The arguments hold no parentheses, so they start at the last one.
		if start := strings.LastIndex(line, "("); start >= 0 && start < len(line)-2 {
			lines[i] = line[:start] + "(...)"
		}
	}

	return strings.Join(lines, "\n")
}

// splitGoroutineDump splits a goroutine dump into chunks of about chunkSize
// bytes. It never splits the stack of one goroutine, so a chunk is larger than
// chunkSize if one stack is.
func splitGoroutineDump(dump string, chunkSize int) []string {
	var (
		chunks  []string
		current strings.Builder
	)

	for goroutine := range strings.SplitSeq(strings.TrimSpace(dump), goroutineSeparator) {
		if goroutine == "" {
			continue
		}

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
