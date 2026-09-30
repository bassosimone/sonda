// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"path/filepath"

	"github.com/bassosimone/sonda/internal/plugins/noc"
)

// Forward declarations from the importable [noc] package.
const (
	bodyBinName     = noc.BodyBinName
	exitcodeTxtName = noc.ExitcodeTxtName
	requestJsonName = noc.RequestJsonName
	stderrTxtName   = noc.StderrTxtName
	stdoutJsonName  = noc.StdoutJsonName
)

// pathsSpanDir returns the spool directory path for a given span ID.
func pathsSpanDir(spoolDir, spanID string) string {
	return filepath.Join(spoolDir, spanID[:4], spanID[4:5], spanID[5:6], spanID)
}

// pathsSpanRequestJSON returns the path to request.json inside a span directory.
func pathsSpanRequestJSON(spanDir string) string {
	return filepath.Join(spanDir, requestJsonName)
}

// pathsSpanStdoutJSON returns the path to stdout.json inside a span directory.
func pathsSpanStdoutJSON(spanDir string) string {
	return filepath.Join(spanDir, stdoutJsonName)
}

// pathsSpanStderrTxt returns the path to stderr.txt inside a span directory.
func pathsSpanStderrTxt(spanDir string) string {
	return filepath.Join(spanDir, stderrTxtName)
}

// pathsSpanExitCodeTxt returns the path to exitcode.txt inside a span directory.
func pathsSpanExitCodeTxt(spanDir string) string {
	return filepath.Join(spanDir, exitcodeTxtName)
}

// pathsSpanDirTmp returns the temporary span directory path (before atomic rename).
func pathsSpanDirTmp(spoolDir, spanID string) string {
	return pathsSpanDir(spoolDir, spanID) + ".tmp"
}

// spanBodyBin returns the path to body.bin inside a span directory.
func spanBodyBin(spanDir string) string {
	return filepath.Join(spanDir, bodyBinName)
}
