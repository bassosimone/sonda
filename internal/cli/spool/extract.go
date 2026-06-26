// SPDX-License-Identifier: GPL-3.0-or-later

package spool

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/paths"
	"github.com/bassosimone/sonda/internal/structured"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
	"github.com/google/uuid"
	parquet "github.com/parquet-go/parquet-go"
)

// extractMain is the main function of the `sonda spool extract` subcommand.
func extractMain(ctx context.Context, args []string) error {
	env := testable.Env

	var (
		maxAge   = 6 * time.Hour
		spoolDir = "."
	)

	fset := vflag.NewFlagSet("sonda spool extract", vflag.ExitOnError)
	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.Stdout
	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.DurationVar(&maxAge, 0, "max-age", "Only extract spans newer than `DURATION`.")
	fset.StringVar(&spoolDir, 0, "spool-dir", "Use `DIR` instead of `@DEFAULT_VALUE@`.")
	runtimex.PanicOnError0(fset.Parse(args))

	cutoff := time.Now().Add(-maxAge)
	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))
	extractWalkDir(logger, spoolDir, cutoff, 3)
	return nil
}

// extractWalkDir walks the spool sharding tree recursively. At depth > 0,
// it descends into subdirectories. At depth 0, it processes span directories.
func extractWalkDir(logger *slog.Logger, dir string, cutoff time.Time, depth int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if depth > 0 {
			child := filepath.Join(dir, e.Name())
			extractWalkDir(logger, child, cutoff, depth-1)
		} else {
			extractMaybeProcessSpan(logger, dir, e.Name(), cutoff)
		}
	}
}

// extractMaybeProcessSpan processes a span directory if its UUIDv7 timestamp
// is within the cutoff. Skips .tmp directories (incomplete spans).
func extractMaybeProcessSpan(logger *slog.Logger, parent, name string, cutoff time.Time) {
	// Skip entry if the data is still being generated.
	if strings.HasSuffix(name, ".tmp") {
		return
	}

	// We only consider valid UUIDv7 entries.
	id, err := uuid.Parse(name)
	if err != nil {
		return
	}
	if id.Version() != 7 {
		return
	}

	// Do not process the entry if it's too old.
	sec, nsec := id.Time().UnixTime()
	ts := time.Unix(sec, nsec)
	if ts.Before(cutoff) {
		return
	}
	spanDir := filepath.Join(parent, name)

	// Do not process the entry if it's already processed.
	if _, err := os.Stat(paths.SpanMetricsParquet(spanDir)); err == nil {
		return
	}

	// Extract events from the `stdout.txt` file.
	rows, err := extractParseSpan(spanDir)
	if err != nil {
		logger.Warn("failed to parse span", slog.String("spanDir", spanDir), slog.Any("err", err))
		return
	}
	if len(rows) <= 0 {
		return
	}

	// Convert to Parquet format and write the file.
	if err := extractWriteParquet(spanDir, rows); err != nil {
		logger.Warn("failed to write metrics", slog.String("spanDir", spanDir), slog.Any("err", err))
		return
	}
	logger.Info("extracted metrics", slog.String("spanDir", spanDir), slog.Int("rows", len(rows)))
}

var extractDoneEvents = map[string]bool{
	"connectDone":       true,
	"tlsHandshakeDone":  true,
	"httpRoundTripDone": true,
	"dnsExchangeDone":   true,
}

func extractParseSpan(spanDir string) ([]structured.Metrics, error) {
	data, err := os.ReadFile(paths.SpanStdout(spanDir))
	if err != nil {
		return nil, err
	}

	var rows []structured.Metrics
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		if len(line) <= 0 {
			continue
		}
		ev, err := structured.ParseEvent(line)
		if err != nil {
			continue
		}
		if !extractDoneEvents[ev.Msg] {
			continue
		}
		rows = append(rows, extractEventToMetrics(ev))
	}
	return rows, nil
}

func extractEventToMetrics(ev *structured.Event) structured.Metrics {
	m := structured.Metrics{
		SpanID:     ev.SpanID,
		Msg:        ev.Msg,
		T0:         ev.T0.UnixMicro(),
		T:          ev.T.UnixMicro(),
		DurationUs: ev.T.Sub(ev.T0).Microseconds(),
		LocalAddr:  ev.LocalAddr,
		RemoteAddr: ev.RemoteAddr,
		Protocol:   ev.Protocol,
	}
	if ev.ErrClass != "" {
		m.ErrClass = &ev.ErrClass
	}
	if ev.ServerProtocol != "" {
		m.ServerProtocol = &ev.ServerProtocol
	}
	if ev.HTTPResponseStatusCode != 0 {
		code := int64(ev.HTTPResponseStatusCode)
		m.HTTPResponseStatusCode = &code
	}
	if ev.ReflexiveAddrV4 != "" {
		m.ReflexiveAddrV4 = &ev.ReflexiveAddrV4
	}
	if ev.ReflexiveAddrV6 != "" {
		m.ReflexiveAddrV6 = &ev.ReflexiveAddrV6
	}
	return m
}

func extractWriteParquet(spanDir string, rows []structured.Metrics) (err error) {
	tmpPath := paths.SpanMetricsParquetTmp(spanDir)
	finalPath := paths.SpanMetricsParquet(spanDir)

	filep, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(tmpPath)
		}
	}()

	w := parquet.NewGenericWriter[structured.Metrics](filep)
	if _, err = w.Write(rows); err != nil {
		filep.Close()
		return err
	}
	if err = w.Close(); err != nil {
		filep.Close()
		return err
	}
	if err = filep.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, finalPath)
}
