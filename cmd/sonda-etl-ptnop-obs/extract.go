// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/bassosimone/sonda/internal/ptnopdata"
	"github.com/bassosimone/sonda/internal/ptnoppaths"
	"github.com/bassosimone/sonda/internal/testable"
)

// extractKeepEvent contains the name of the events to keep.
var extractKeepEvent = map[string]bool{
	"connectDone":        true,
	"dnsResponse":        true,
	"tlsHandshakeDone":   true,
	"httpBodyStreamDone": true,
	"httpRoundTripDone":  true,
	"dnsExchangeDone":    true,
}

// extractSpanDir extracts the `spanDir` stdout into the `metricsDir`.
//
// Returns the number of lines written and/or an error.
func extractSpanDir(
	env *testable.Environ,
	logger *slog.Logger,
	obsMetricsDir,
	spanDir string,
	ts time.Time,
) (int64, error) {
	// Open the source file: `stdout.txt` inside the current span dir.
	inputPath := ptnoppaths.SpanStdout(spanDir)
	source, err := env.OpenFile(inputPath, os.O_RDONLY, 0)
	if err != nil {
		return 0, err
	}
	defer source.Close()

	// Open the destination file.
	dateOnlyTimestampUTC := ts.UTC().Format("20060102") + "T000000Z"
	outputPath := filepath.Join(obsMetricsDir, dateOnlyTimestampUTC+".jsonl")
	dest, err := env.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0640)
	if err != nil {
		return 0, err
	}

	// Extract the data.
	numWritten, err := extractSpanLoop(dest, logger, source, inputPath)
	if err != nil {
		dest.Close()
		return numWritten, err
	}

	// Make sure we close the output file.
	return numWritten, dest.Close()
}

// extractMaxLineSize is the maximum accepted line size.
const extractMaxLineSize = 1 << 19

// extractSpanLoop copies the selected lines from source to dest.
//
// Returns the number of lines written and/or an error.
func extractSpanLoop(
	dest io.Writer,
	logger *slog.Logger,
	sourceFp io.Reader,
	sourcePath string,
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
		ev, err := ptnopdata.ParseEvent(line)
		if err != nil {
			logger.Warn(
				"ptnopdata.ParseEvent",
				slog.Any("err", err),
				slog.String("file", sourcePath),
				slog.Int64("line", lineID),
			)
			continue
		}
		if !extractKeepEvent[ev.Msg] {
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
