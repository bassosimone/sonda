// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"log/slog"
	"os"

	"github.com/bassosimone/sonda/internal/ptnopdata"
	"github.com/bassosimone/sonda/internal/ptnoppaths"
	"github.com/bassosimone/sonda/internal/testable"
)

// updateMaxLineSize is the maximum accepted line size.
const updateMaxLineSize = 1 << 19

// updateMetrics updates Prometheus metrics based on the spanDir content.
//
// Returns the number of processed events and/or an error.
func updateMetrics(
	env *testable.Environ,
	logger *slog.Logger,
	spanDir string,
) (int64, error) {
	// Open the source file: `stdout.txt` inside the current span dir.
	inputPath := ptnoppaths.SpanStdout(spanDir)
	source, err := env.OpenFile(inputPath, os.O_RDONLY, 0)
	if err != nil {
		return 0, err
	}
	defer source.Close()

	// Create the scanner.
	scanner := bufio.NewScanner(source)
	scanner.Buffer(nil, updateMaxLineSize)

	// Walk through all input lines.
	var (
		lineID       int64
		numProcessed int64
	)
	for scanner.Scan() {
		line := scanner.Bytes()
		lineID += 1
		if len(line) <= 0 {
			continue
		}

		ev, err := ptnopdata.ParseEvent(line)
		if err != nil {
			logger.Warn(
				"ptnopdata.ParseEvent",
				slog.Any("err", err),
				slog.String("file", inputPath),
				slog.Int64("line", lineID),
			)
			continue
		}

		numProcessed += 1
		// TODO(bassosimone): do something with the event
		_ = ev
	}

	// Bail if we could not read the whole file.
	if err := scanner.Err(); err != nil {
		return numProcessed, err
	}

	// Update Prometheus metrics and return.
	// TODO(bassosimone): implement
	return numProcessed, nil
}
