// SPDX-License-Identifier: GPL-3.0-or-later

// Package paths contains spool directory path construction.
package paths

import "path/filepath"

// SpanDir returns the spool directory path for a given span ID.
func SpanDir(spoolDir, spanID string) string {
	return filepath.Join(spoolDir, spanID[:4], spanID[4:5], spanID[5:6], spanID)
}

// SpanArgvJSON returns the path to argv.json inside a span directory.
func SpanArgvJSON(spanDir string) string {
	return filepath.Join(spanDir, "argv.json")
}

// SpanStdout returns the path to stdout.txt inside a span directory.
func SpanStdout(spanDir string) string {
	return filepath.Join(spanDir, "stdout.txt")
}

// SpanStderr returns the path to stderr.txt inside a span directory.
func SpanStderr(spanDir string) string {
	return filepath.Join(spanDir, "stderr.txt")
}

// SpanExitCode returns the path to exitcode.txt inside a span directory.
func SpanExitCode(spanDir string) string {
	return filepath.Join(spanDir, "exitcode.txt")
}
