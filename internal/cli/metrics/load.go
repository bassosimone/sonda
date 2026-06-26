// SPDX-License-Identifier: GPL-3.0-or-later

package metrics

import (
	"context"
	"errors"
	"io"
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

// loadMain is the main function of the `sonda metrics load` subcommand.
func loadMain(ctx context.Context, args []string) error {
	env := testable.Env

	var (
		maxAge     = 24 * time.Hour
		metricsDir = "."
		spoolDir   = "."
	)

	fset := vflag.NewFlagSet("sonda metrics load", vflag.ExitOnError)
	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.Stdout
	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.DurationVar(&maxAge, 0, "max-age", "Ignore spans older than `DURATION`.")
	fset.StringVar(&metricsDir, 0, "metrics-dir", "Write daily Parquet files to `DIR` instead of `@DEFAULT_VALUE@`.")
	fset.StringVar(&spoolDir, 0, "spool-dir", "Read span metrics from `DIR` instead of `@DEFAULT_VALUE@`.")
	runtimex.PanicOnError0(fset.Parse(args))

	cutoff := time.Now().Add(-maxAge)
	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))
	loadWalkDir(logger, spoolDir, metricsDir, cutoff, 3)
	return nil
}

// loadWalkDir walks the spool sharding tree recursively. At depth > 0,
// it descends into subdirectories. At depth 0, it processes span directories.
func loadWalkDir(logger *slog.Logger, dir, metricsDir string, cutoff time.Time, depth int) {
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
			loadWalkDir(logger, child, metricsDir, cutoff, depth-1)
		} else {
			loadMaybeProcessSpan(logger, dir, metricsDir, e.Name(), cutoff)
		}
	}
}

// loadMaybeProcessSpan loads a span's metrics into the daily aggregate
// if the span has metrics.parquet and hasn't been loaded yet. Skips
// .tmp directories (incomplete spans).
func loadMaybeProcessSpan(logger *slog.Logger, parent, metricsDir, name string, cutoff time.Time) {
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

	// Do not process the entry if metrics have not been extracted yet.
	metricsPath := paths.SpanMetricsParquet(spanDir)
	if _, err := os.Stat(metricsPath); err != nil {
		return
	}

	// Atomically claim this span using O_CREATE|O_EXCL so that
	// concurrent loaders cannot process the same span twice.
	sentinelPath := paths.SpanMetricsLoaded(spanDir)
	sentinel, err := os.OpenFile(sentinelPath, os.O_CREATE|os.O_EXCL, 0640)
	if err != nil {
		return
	}
	sentinel.Close()

	// Read rows from the span's metrics.parquet.
	rows, err := loadReadSpanMetrics(metricsPath)
	if err != nil {
		logger.Warn("failed to read span metrics", slog.String("spanDir", spanDir), slog.Any("err", err))
		os.Remove(sentinelPath) // cleanup the sentinel on failure
		return
	}
	if len(rows) <= 0 {
		os.Remove(sentinelPath) // cleanup the sentinel on failure
		return
	}

	// Append rows to the daily aggregate file.
	day := ts.UTC().Format("2006-01-02")
	if err := loadAppendDaily(metricsDir, day, rows); err != nil {
		logger.Warn("failed to append to daily metrics", slog.String("day", day), slog.Any("err", err))
		os.Remove(sentinelPath) // cleanup the sentinel on failure
		return
	}
	logger.Info("loaded metrics", slog.String("spanDir", spanDir), slog.String("day", day), slog.Int("rows", len(rows)))
}

// loadReadSpanMetrics reads all rows from a span's metrics.parquet file.
func loadReadSpanMetrics(path string) ([]structured.Metrics, error) {
	filep, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer filep.Close()

	info, err := filep.Stat()
	if err != nil {
		return nil, err
	}

	pf, err := parquet.OpenFile(filep, info.Size())
	if err != nil {
		return nil, err
	}

	reader := parquet.NewGenericReader[structured.Metrics](pf)
	defer reader.Close()
	rows := make([]structured.Metrics, reader.NumRows())

	count, err := reader.Read(rows)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return rows[:count], nil
}

// loadDailyPath returns the path to a daily aggregate Parquet file:
// metricsDir/YYYY/MM/DD/YYYY-MM-DD.parquet
func loadDailyPath(metricsDir, day string) string {
	t, _ := time.Parse("2006-01-02", day)
	return filepath.Join(
		metricsDir,
		t.Format("2006"),
		t.Format("01"),
		t.Format("02"),
		day+".parquet",
	)
}

// TODO(bassosimone): loadAppendDaily is O(n²) for bulk loads because it
// reads and rewrites the daily file for every span. Batch all spans by day
// first, then write each daily file once.

// loadAppendDaily appends rows to the daily aggregate Parquet file,
// reading existing rows first if the file already exists.
func loadAppendDaily(metricsDir, day string, newRows []structured.Metrics) error {
	dailyPath := loadDailyPath(metricsDir, day)

	// Read existing rows if the daily file already exists.
	var existing []structured.Metrics
	if _, err := os.Stat(dailyPath); err == nil {
		existing, err = loadReadSpanMetrics(dailyPath)
		if err != nil {
			return err
		}
	}

	allRows := append(existing, newRows...)

	// Ensure the directory exists.
	dir := filepath.Dir(dailyPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}

	// Write via tmp + rename for atomicity.
	tmpPath := dailyPath + ".tmp"
	filep, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(tmpPath)
		}
	}()

	w := parquet.NewGenericWriter[structured.Metrics](filep, parquet.Compression(&parquet.Zstd))
	if _, err = w.Write(allRows); err != nil {
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

	return os.Rename(tmpPath, dailyPath)
}
