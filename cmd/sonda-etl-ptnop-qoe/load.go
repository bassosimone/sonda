// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
	"github.com/google/uuid"
	parquet "github.com/parquet-go/parquet-go"
	"github.com/rogpeppe/go-internal/lockedfile"
)

// loadMain is the main function of the `sonda-etl-ptnop-qoe load` subcommand.
func loadMain(ctx context.Context, args []string) error {
	env := testable.ContextEnviron(ctx)

	var (
		maxAge     = 24 * time.Hour
		metricsDir = "."
		spoolDir   = "."
	)

	fset := vflag.NewFlagSet("sonda-etl-ptnop-qoe load", vflag.ExitOnError)

	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.UsageStdout

	upr := vflag.NewDefaultUsagePrinter()
	fset.UsagePrinter = upr
	upr.AddDescription(
		"Append the rows of each span's `qoe.parquet` to the daily file " +
			"`<metrics-dir>/YYYY/MM/DD/YYYY-MM-DD.parquet`, choosing the day from " +
			"the span's UTC timestamp. A `qoe.loaded` file marks the spans already " +
			"loaded, and a lock file in `--metrics-dir` serializes concurrent runs.")

	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.DurationVar(&maxAge, 0, "max-age", "Ignore spans older than `DURATION`.")
	fset.StringVar(&metricsDir, 0, "metrics-dir", "Write daily Parquet files to `DIR` instead of `@DEFAULT_VALUE@`.")
	fset.StringVar(&spoolDir, 0, "spool-dir", "Read span metrics from `DIR` instead of `@DEFAULT_VALUE@`.")

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using ExitOnError

	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))

	// Serialize concurrent loads. The per-span sentinel prevents loading
	// the same span twice, but two loaders handling different spans of the
	// same day would both rewrite the daily file and the last rename wins,
	// losing the other loader's rows. The lock lives inside the metrics dir
	// because it protects that dir and follows `--metrics-dir`.
	if err := os.MkdirAll(metricsDir, 0750); err != nil {
		logger.Error("failed to create metrics directory", slog.Any("err", err))
		env.Exit(1)
	}
	unlock, err := lockedfile.MutexAt(filepath.Join(metricsDir, "lock")).Lock()
	if err != nil {
		logger.Error("failed to lock metrics directory", slog.Any("err", err))
		env.Exit(1)
	}
	defer unlock()

	// Collect the candidate spans grouped by UTC day, then load each
	// day with a single rewrite of its daily file. Processing days in
	// sorted order makes the logs easier to follow.
	cutoff := time.Now().Add(-maxAge)
	byDay := make(map[string][]string)
	loadWalkDir(spoolDir, cutoff, byDay, 3)
	for _, day := range slices.Sorted(maps.Keys(byDay)) {
		loadProcessDay(logger, metricsDir, day, byDay[day])
	}
	return nil
}

// loadWalkDir walks the spool sharding tree recursively. At depth > 0,
// it descends into subdirectories. At depth 0, it collects span directories.
func loadWalkDir(dir string, cutoff time.Time, byDay map[string][]string, depth int) {
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
			loadWalkDir(child, cutoff, byDay, depth-1)
		} else {
			loadMaybeCollectSpan(dir, e.Name(), cutoff, byDay)
		}
	}
}

// loadMaybeCollectSpan adds the span directory to byDay, keyed by the
// span's UTC day, if the span has qoe.parquet. Skips .tmp directories
// (incomplete spans). Already loaded spans are collected as well and
// skipped later by [loadProcessDay] when claiming them.
func loadMaybeCollectSpan(parent, name string, cutoff time.Time, byDay map[string][]string) {
	// Skip entry if the data is still being generated.
	if strings.HasSuffix(name, ".tmp") {
		return
	}

	// We only consider valid UUIDv7 entries.
	spanID, err := uuid.Parse(name)
	if err != nil {
		return
	}
	if spanID.Version() != 7 {
		return
	}

	// Do not process the entry if it's too old.
	sec, nsec := spanID.Time().UnixTime()
	ts := time.Unix(sec, nsec)
	if ts.Before(cutoff) {
		return
	}
	spanDir := filepath.Join(parent, name)

	// Do not process the entry if metrics have not been extracted yet.
	metricsPath := spanMetricsParquet(spanDir)
	if _, err := os.Stat(metricsPath); err != nil {
		return
	}

	// Collect the span under its UTC day.
	day := ts.UTC().Format("2006-01-02")
	byDay[day] = append(byDay[day], spanDir)
}

// loadProcessDay loads the metrics of the given spans, which all belong
// to the given UTC day, into the daily aggregate with a single rewrite.
func loadProcessDay(logger *slog.Logger, metricsDir, day string, spanDirs []string) {
	// 1. Claim each span and read its rows. Spans we cannot read, or
	// that have no rows, are released so that a later run retries them.
	var (
		rows      []metricsRow
		sentinels []string
	)
	for _, spanDir := range spanDirs {
		// Claim this span by creating its sentinel with O_CREATE|O_EXCL,
		// which fails if a previous run already loaded the span.
		sentinelPath := spanMetricsLoaded(spanDir)
		sentinel, err := os.OpenFile(sentinelPath, os.O_CREATE|os.O_EXCL, 0640)
		if err != nil {
			continue
		}
		sentinel.Close()

		spanRows, err := loadReadSpanMetrics(spanMetricsParquet(spanDir))
		if err != nil {
			logger.Warn("failed to read span metrics", slog.String("spanDir", spanDir), slog.Any("err", err))
			os.Remove(sentinelPath) // cleanup the sentinel on failure
			continue
		}
		if len(spanRows) <= 0 {
			os.Remove(sentinelPath) // cleanup the sentinel on failure
			continue
		}
		rows = append(rows, spanRows...)
		sentinels = append(sentinels, sentinelPath)
	}
	if len(rows) <= 0 {
		return
	}

	// 2. Append all the rows to the daily aggregate file at once. On
	// failure, release all the claimed spans so a later run retries them.
	if err := loadAppendDaily(metricsDir, day, rows); err != nil {
		logger.Warn("failed to append to daily metrics", slog.String("day", day), slog.Any("err", err))
		for _, sentinelPath := range sentinels {
			os.Remove(sentinelPath) // cleanup the sentinel on failure
		}
		return
	}
	logger.Info("loaded metrics", slog.String("day", day), slog.Int("spans", len(sentinels)), slog.Int("rows", len(rows)))
}

// loadReadSpanMetrics reads all rows from a span's qoe.parquet file.
func loadReadSpanMetrics(path string) ([]metricsRow, error) {
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

	reader := parquet.NewGenericReader[metricsRow](pf)
	defer reader.Close()
	rows := make([]metricsRow, reader.NumRows())

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

// loadAppendDaily appends rows to the daily aggregate Parquet file,
// reading existing rows first if the file already exists.
func loadAppendDaily(metricsDir, day string, newRows []metricsRow) error {
	dailyPath := loadDailyPath(metricsDir, day)

	// Read existing rows if the daily file already exists.
	var existing []metricsRow
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

	w := parquet.NewGenericWriter[metricsRow](filep, parquet.Compression(&parquet.Zstd))
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
