// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/cmd/internal/plugincommand"
	"github.com/bassosimone/sonda/internal/config"
	"github.com/bassosimone/sonda/internal/spool"
	"github.com/bassosimone/sonda/internal/spoolfs"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/sonda/internal/triggersreader"
	"github.com/bassosimone/vflag"
)

func main() {
	plugincommand.Main(0, realMain)
}

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

	// Open the root spool dir using the spool API.
	spoolRootDir, err := spoolfs.NewRootDir(env, spoolDir)
	if err != nil {
		logger.Error("spoolfs.NewRootDir", slog.Any("err", err))
		env.Exit(1)
	}

	// Initialize the reading loop.
	loop, err := triggersreader.NewLoop(
		"ptnop", // dataType
		env,
		logger,
		maxSpanAge,
		filepath.Join(runDir, "etl-ptnop-archive"),
		spoolRootDir,
	)
	if err != nil {
		logger.Error("triggersreader.NewLoop", slog.Any("err", err))
		env.Exit(1)
	}
	defer loop.Close()

	// Make sure the destination directory exists.
	ptnopArchiveDir := filepath.Join(archiveDir, "ptnop")
	if err := env.MkdirAll(ptnopArchiveDir, 0750); err != nil {
		logger.Error("env.MkdirAll", slog.Any("err", err))
		env.Exit(1)
	}

	// Loop processing the triggers.
	loop.Run(
		ctx,
		func(spanDir spool.SpanDir) error {
			numWritten, err := extractSpanDir(env, logger, ptnopArchiveDir, spanDir)
			if err != nil {
				return fmt.Errorf("extractSpanDir: %s: %w", spanDir.ID(), err)
			}
			logger.Info(
				"extractSpanDir",
				slog.Int64("numEventsWritten", numWritten),
				slog.String("spanId", spanDir.ID()),
			)
			return nil
		},
		func(cutoff time.Time) error {
			// Compress the days no span can be appended to anymore.
			_ = compressClosedDays(env, logger, ptnopArchiveDir, cutoff)

			// Remove the days older than the retention period.
			if maxDays > 0 {
				expireCutoff := time.Now().UTC().AddDate(0, 0, -maxDays)
				_ = expireOldDays(env, logger, ptnopArchiveDir, expireCutoff)
			}
			return nil
		})
	return nil
}
