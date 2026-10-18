// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/cmd/internal/plugincommand"
	"github.com/bassosimone/sonda/internal/config"
	"github.com/bassosimone/sonda/internal/spool"
	"github.com/bassosimone/sonda/internal/spoolfs"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/sonda/internal/triggers"
	"github.com/bassosimone/vflag"
	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"
)

func main() {
	plugincommand.Main(0, realMain)
}

// drainMaxLineSize is the maximum accepted line size.
const drainMaxLineSize = 1 << 19

func realMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Set the command defaults.
	presets := config.Defaults()
	configErr := config.ReadDefaultsInto(env, presets)
	var (
		maxDays    = presets.Archive.Ptnop.MaxDays
		maxSpanAge = 24 * time.Hour
		archiveDir = config.ArchiveDir
		spoolDir   = config.SpoolDir
		runDir     = config.RunDir
	)

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda-etl-ptnop-archive", vflag.ExitOnError)

	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.UsageStdout

	upr := vflag.NewDefaultUsagePrinter()
	fset.UsagePrinter = upr
	upr.AddDescription(
		"Archive the events in ptnop logs.",
		"Read triggers from `$runDir/etl-ptnop-archive`.",
		"Read the corresponding ptnop data from `$spoolDir/ptnop`.",
		"Archive ptnop data to `$archiveDir/ptnop`.",
	)

	fset.AutoHelp('h', "help", "Show this help message and exit.")

	fset.StringVar(&archiveDir, 0, "archive-dir",
		"Top-level archive `DIR` containing the data type directories.",
		"Default: `@DEFAULT_VALUE@`.")

	fset.IntVar(&maxDays, 0, "max-days",
		"Remove the daily files older than `DAYS`. Zero means never.",
		"Default: `@DEFAULT_VALUE@`.")

	fset.DurationVar(&maxSpanAge, 0, "max-span-age",
		"Ignore spans older than `DURATION`.")

	fset.StringVar(&runDir, 0, "run-dir",
		"Top-level run `DIR` containing the trigger directories.",
		"Default: `@DEFAULT_VALUE@`.")

	fset.StringVar(&spoolDir, 0, "spool-dir",
		"Top-level spool `DIR` containing the data type directories.",
		"Default: `@DEFAULT_VALUE@`.")

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using ExitOnError

	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))

	// Defer reporting config errors after flag parsing to honor `-h/--help`.
	if configErr != nil {
		logger.Error("config.ReadDefaultsInto", slog.Any("err", configErr))
		env.Exit(1)
	}
	if maxDays < 0 {
		logger.Error("invalid --max-days", slog.Int("maxDays", maxDays))
		env.Exit(2)
	}

	// Setup the watcher to watch the triggers directory.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Error("fsnotify.NewWatcher", slog.Any("err", err))
		env.Exit(1)
	}
	etlPtnopArchiveRunDir := filepath.Join(runDir, "etl-ptnop-archive")
	defer watcher.Close()
	if err := watcher.Add(etlPtnopArchiveRunDir); err != nil {
		logger.Error("watcher.Add", slog.Any("err", err))
		env.Exit(1)
	}

	// Make sure the destination directory exists.
	ptnopArchiveDir := filepath.Join(archiveDir, "ptnop")
	if err := env.MkdirAll(ptnopArchiveDir, 0750); err != nil {
		logger.Error("env.MkdirAll", slog.Any("err", err))
		env.Exit(1)
	}

	// Open the root spool directory using the spool API.
	spoolRootDir, err := spoolfs.NewRootDir(env, spoolDir)
	if err != nil {
		logger.Error("spoolfs.NewRootDir", slog.Any("err", err))
		env.Exit(1)
	}

	// Loop processing the triggers.
	processTriggersLoop(
		ctx,
		env,
		etlPtnopArchiveRunDir,
		logger,
		maxDays,
		maxSpanAge,
		ptnopArchiveDir,
		spoolRootDir,
		watcher,
	)
	return nil
}

// processTriggersLoop is the loop that processes the triggers dir in a loop.
func processTriggersLoop(
	ctx context.Context,
	env *testable.Environ,
	etlPtnopArchiveRunDir string,
	logger *slog.Logger,
	maxDays int,
	maxSpanAge time.Duration,
	ptnopArchiveDir string,
	spoolRootDir spool.RootDir,
	watcher *fsnotify.Watcher,
) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		cutoff := time.Now().Add(-maxSpanAge)

		// Process new trigger files.
		_ = processTriggersDir(
			cutoff,
			env,
			etlPtnopArchiveRunDir,
			logger,
			ptnopArchiveDir,
			spoolRootDir,
		)

		// Compress the days no span can be appended to anymore.
		_ = compressClosedDays(env, logger, ptnopArchiveDir, cutoff)

		// Remove the days older than the retention period.
		if maxDays > 0 {
			expireCutoff := time.Now().UTC().AddDate(0, 0, -maxDays)
			_ = expireOldDays(env, logger, ptnopArchiveDir, expireCutoff)
		}

		// Block until a new event occurs.
		if !processWaitEvents(ctx, ticker, watcher) {
			break
		}
	}

}

// processWaitEvents blocks until a context, ticker, or watcher event occurs.
//
// Returns false when the caller should stop waiting for events.
func processWaitEvents(ctx context.Context, ticker *time.Ticker, watcher *fsnotify.Watcher) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			return true
		case err, ok := <-watcher.Errors:
			runtimex.Assert(!ok || errors.Is(err, fsnotify.ErrEventOverflow))
			return ok
		case event, ok := <-watcher.Events:
			if ok && event.Has(fsnotify.Create) {
				return true
			}
		}
	}
}

// processTriggersDir does a pass over `/run/sonda/etl-ptnop-archive`, processing
// and removing each trigger file, and generating archives as a side effect.
func processTriggersDir(
	cutoff time.Time,
	env *testable.Environ,
	etlPtnopArchiveRunDir string,
	logger *slog.Logger,
	ptnopArchiveDir string,
	spoolRootDir spool.RootDir,
) error {
	logger.Info("processing triggers dir", slog.String("dir", etlPtnopArchiveRunDir))

	dentries, err := env.ReadDir(etlPtnopArchiveRunDir)
	if err != nil {
		logger.Error("env.ReadDir", slog.Any("err", err))
		return err
	}

	var errv []error
	for _, dentry := range dentries {
		err := processDentry(
			cutoff,
			dentry,
			env,
			etlPtnopArchiveRunDir,
			logger,
			ptnopArchiveDir,
			spoolRootDir,
		)
		if err != nil {
			logger.Warn("processDentry", slog.Any("err", err))
			errv = append(errv, err)
			continue
		}
	}

	return errors.Join(errv...)
}

// processDentry drains a single dentry in the `/run/sonda/etl-ptnop-archive` dir.
func processDentry(
	cutoff time.Time,
	dentry fs.DirEntry,
	env *testable.Environ,
	etlPtnopArchiveRunDir string,
	logger *slog.Logger,
	ptnopArchiveDir string,
	spoolRootDir spool.RootDir,
) error {
	// 1. The dentry must be a regular `<UUIDv7>.jsonl` file.
	name, mode := dentry.Name(), dentry.Type()
	fullFilePath := filepath.Join(etlPtnopArchiveRunDir, name)
	if !mode.IsRegular() {
		return fmt.Errorf("not a regular file: %s", fullFilePath)
	}
	spanID, err := uuid.Parse(strings.TrimSuffix(name, ".jsonl"))
	if err != nil {
		return fmt.Errorf("not a UUID file name: %s: %w", fullFilePath, err)
	}
	if spanID.Version() != 7 {
		return fmt.Errorf("invalid UUID version: %s", fullFilePath)
	}

	// 2. From now on, we remove the file on exit/panic.
	defer env.Remove(fullFilePath)

	// 3. The file must have been created after the cutoff.
	sec, nsec := spanID.Time().UnixTime()
	ts := time.Unix(sec, nsec)
	if ts.Before(cutoff) {
		logger.Warn("skip old dentry", slog.String("dentry", fullFilePath))
		return nil
	}

	// 4. Play it safe and read the file line by line. We assume the file
	// is reasonably small, but it is best to write robust code here.
	filep, err := env.OpenFile(fullFilePath, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("env.OpenFile: %w", err)
	}
	defer filep.Close()
	scanner := bufio.NewScanner(filep)
	scanner.Buffer(nil, drainMaxLineSize)

	var errv []error
	for scanner.Scan() {
		// 4.1. Parse information about a created span and fail hard if
		// the parsing fails: it means a corrupt trigger file.
		var info triggers.CreatedSpan
		if err := json.Unmarshal(scanner.Bytes(), &info); err != nil {
			return fmt.Errorf("%s: %w", fullFilePath, err)
		}

		// 4.2. Otherwise process each span and collect errors to be
		// reported all together at the end of the processing.
		err := processSpanTrigger(cutoff, env, &info, logger, ptnopArchiveDir, spoolRootDir)
		if err != nil {
			logger.Warn("cannot process span", slog.Any("err", err))
			errv = append(errv, err)
			continue
		}
	}

	// 5. Handle errors that may have occurred.
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner.Err: %w", err)
	}
	if len(errv) >= 1 {
		return errors.New("failed to process at least one span")
	}
	return nil
}

// processSpanTrigger processes a single created span.
func processSpanTrigger(
	cutoff time.Time,
	env *testable.Environ,
	info *triggers.CreatedSpan,
	logger *slog.Logger,
	ptnopArchiveDir string,
	spoolRootDir spool.RootDir,
) error {
	// 1. The span must be about "ptnop".
	if info.DataType != "ptnop" {
		logger.Info("skip unhandled data type", slog.String("dataType", info.DataType))
		return nil
	}

	// 2. Make sure the spanID is valid and created after the cutoff.
	spanDir, err := spool.OpenSpanDir(spoolRootDir, "ptnop", info.SpanID)
	if err != nil {
		return fmt.Errorf("spool.OpenSpanDir: %w", err)
	}
	if spanDir.Created().Before(cutoff) {
		logger.Info("skip old span", slog.String("spanID", info.SpanID))
		return nil
	}

	// 3. Extract events from the span `stdout.txt` entity.
	numWritten, err := extractSpanDir(env, logger, ptnopArchiveDir, spanDir)
	if err != nil {
		return fmt.Errorf("extractSpanDir: %s: %w", info.SpanID, err)
	}
	logger.Info(
		"extractSpanDir",
		slog.Int64("numEventsWritten", numWritten),
		slog.String("spanId", info.SpanID),
	)
	return nil
}
