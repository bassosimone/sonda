// SPDX-License-Identifier: GPL-3.0-or-later

// Package ptnoppaths contains the ptnop spool directory path construction.
package ptnoppaths

import "path/filepath"

// TODO(bassosimone): the SpanDir, SpanArgvJSON, SpanStdout, SpanStderr,
// SpanExitCode, and SpanTmpDir should probably not be here:
//
// - SpanDir and SpanTmpDir are general spool properties
//
// - the other definitions are part of the spool-run contract but are
//   otherwise unused by code using serve-ptnop

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

// SpanStdoutGz returns the path to stdout.txt.gz inside a span directory.
func SpanStdoutGz(spanDir string) string {
	return filepath.Join(spanDir, "stdout.txt.gz")
}

// SpanStderr returns the path to stderr.txt inside a span directory.
func SpanStderr(spanDir string) string {
	return filepath.Join(spanDir, "stderr.txt")
}

// SpanExitCode returns the path to exitcode.txt inside a span directory.
func SpanExitCode(spanDir string) string {
	return filepath.Join(spanDir, "exitcode.txt")
}

// SpanDirTmp returns the temporary span directory path (before atomic rename).
func SpanDirTmp(spoolDir, spanID string) string {
	return SpanDir(spoolDir, spanID) + ".tmp"
}

// SpanBodyBinGz returns the path to body.bin.gz inside a span directory.
func SpanBodyBinGz(spanDir string) string {
	return filepath.Join(spanDir, "body.bin.gz")
}
