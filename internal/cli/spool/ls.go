// SPDX-License-Identifier: GPL-3.0-or-later

package spool

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
	"github.com/google/uuid"
)

// lsMain is the main function of the `sonda-spool ls` subcommand.
func lsMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)
	logger := slog.New(slog.NewJSONHandler(env.Stdout, nil))

	// Set command defaults.
	var (
		spoolDir = env.Getenv("SONDA_SPOOL_DIR")
	)

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda-spool ls", vflag.ExitOnError)
	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.UsageStdout
	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.StringVar(&spoolDir, 0, "spool-dir", "Use `DIR` instead of `$SONDA_SPOOL_DIR`.")
	fset.SetMinMaxPositionalArgs(0, 0)
	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Refuse to guess the spool directory.
	if spoolDir == "" {
		err := errors.New("neither --spool-dir nor SONDA_SPOOL_DIR is set")
		fset.PrintUsageError(env.Stderr, err)
		env.Exit(2)
	}

	// A spool directory we cannot read is an error for a plumbing tool:
	// the caller must not mistake it for an empty spool.
	if err := lsWalkDir(env, logger, spoolDir, 3); err != nil {
		logger.Error("failed to read spool directory", slog.Any("err", err))
		env.Exit(1)
	}
	return nil
}

func lsWalkDir(env *testable.Environ, logger *slog.Logger, dir string, depth int) error {
	entries, err := env.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		child := filepath.Join(dir, entry.Name())
		if depth > 0 {
			if err := lsWalkDir(env, logger, child, depth-1); err != nil {
				logger.Warn(
					"failed to read directory",
					slog.String("path", child),
					slog.Any("err", err),
				)
			}
			continue
		}

		lsMaybeEmitSpan(logger, child, entry.Name())
	}

	return nil
}

func lsMaybeEmitSpan(logger *slog.Logger, spanDir string, name string) {
	// Skip spans still being written and anything that is not a span.
	if strings.HasSuffix(name, ".tmp") {
		return
	}
	if id, err := uuid.Parse(name); err != nil || id.Version() != 7 {
		return
	}

	// Emit the span directory.
	logger.Info(
		"spanDirEntry",
		slog.String("spanDir", spanDir),
		slog.String("spanId", name),
	)
}
