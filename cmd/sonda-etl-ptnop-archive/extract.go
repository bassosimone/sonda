// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/bassosimone/sonda/internal/ptnopdata"
	"github.com/bassosimone/sonda/internal/spool"
	"github.com/bassosimone/sonda/internal/testable"
)

// extractSpanDir extracts the `spanDir` stdout into the `ptnopArchiveDir`.
//
// Returns the number of lines written and/or an error.
func extractSpanDir(
	env *testable.Environ,
	logger *slog.Logger,
	ptnopArchiveDir string,
	spanDir spool.SpanDir,
) (int64, error) {
	// Open the source entity: `stdout.txt` inside the current span.
	source, err := spanDir.OpenEntity("stdout.txt")
	if err != nil {
		return 0, err
	}
	defer source.Close()

	// Open the destination file.
	dateOnlyTimestampUTC := spanDir.Created().UTC().Format("20060102") + "T000000Z"
	outputPath := filepath.Join(ptnopArchiveDir, dateOnlyTimestampUTC+".jsonl")
	dest, err := env.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0640)
	if err != nil {
		return 0, err
	}

	// Extract the data.
	numWritten, err := extractSpanLoop(dest, logger, source, spanDir)
	if err != nil {
		dest.Close()
		return numWritten, err
	}

	// Make sure we close the output file.
	return numWritten, dest.Close()
}

// extractMaxLineSize is the maximum accepted line size.
const extractMaxLineSize = 1 << 19

// extractSpanLoop copies the lines that parse as events from source to dest.
//
// Returns the number of lines written and/or an error.
func extractSpanLoop(
	dest io.Writer,
	logger *slog.Logger,
	sourceFp io.Reader,
	spanDir spool.SpanDir,
) (int64, error) {
	// Create the scanner.
	scanner := bufio.NewScanner(sourceFp)
	scanner.Buffer(nil, extractMaxLineSize)

	// Walk through all input lines.
	var (
		lineID     int64
		numWritten int64
	)
	for scanner.Scan() {
		line := scanner.Bytes()
		lineID += 1
		if len(line) <= 0 {
			continue
		}
		_, err := ptnopdata.ParseEvent(line)
		if err != nil {
			logger.Warn(
				"ptnopdata.ParseEvent",
				slog.Any("err", err),
				slog.Int64("line", lineID),
				slog.String("spanID", spanDir.ID()),
			)
			continue
		}
		if _, err := dest.Write(append(line, '\n')); err != nil {
			return numWritten, err
		}
		numWritten += 1
	}

	// Return a potential scanner error.
	return numWritten, scanner.Err()
}
