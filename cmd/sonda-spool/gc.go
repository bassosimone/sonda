// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
	"github.com/google/uuid"
)

// gcMain is the main function of the `sonda-spool gc` subcommand.
func gcMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Set command defaults.
	var (
		maxAge   = 6 * time.Hour
		spoolDir = "."
	)

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda-spool gc", vflag.ExitOnError)

	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.UsageStdout

	upr := vflag.NewDefaultUsagePrinter()
	fset.UsagePrinter = upr
	upr.AddDescription(
		"Remove span directories whose UUIDv7 timestamp is older than " +
			"`--max-age`, including incomplete `.tmp` ones, for every data type " +
			"directory under `--spool-dir` (e.g., `<spool-dir>/ptnop`). Also " +
			"remove the sharding directories left empty.")

	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.DurationVar(&maxAge, 0, "max-age", "Remove spans older than `DURATION`.")
	fset.StringVar(&spoolDir, 0, "spool-dir",
		"Top-level spool `DIR` containing the data type directories.",
		"Default: `@DEFAULT_VALUE@`.")

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Compute the cutoff time.
	cutoff := time.Now().Add(-maxAge)
	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))

	// Walk the spool structure: spoolDir/<dataType>/XXXX/X/X/<spanID>.
	gcWalkSpool(logger, spoolDir, cutoff)
	return nil
}

// gcWalkSpool treats each subdirectory of the spool dir as a data type
// directory (e.g., `ptnop`) and walks its sharding tree.
//
// We never remove data type directories, even when they become empty, since
// they are part of the spool structure rather than sharding artifacts.
func gcWalkSpool(logger *slog.Logger, spoolDir string, cutoff time.Time) {
	entries, err := os.ReadDir(spoolDir)
	if err != nil {
		logger.Warn("os.ReadDir", slog.String("path", spoolDir), slog.Any("err", err))
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		gcWalkDir(logger, filepath.Join(spoolDir, e.Name()), cutoff, 3)
	}
}

// gcWalkDir walks a data type's sharding tree (XXXX/X/X/<spanID>) recursively.
// At depth > 0, it descends into subdirectories and removes empty ones. At depth
// 0, it processes span directories.
func gcWalkDir(logger *slog.Logger, dir string, cutoff time.Time, depth int) {
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
			gcWalkDir(logger, child, cutoff, depth-1)
			os.Remove(child)
		} else {
			gcMaybeRemoveSpan(logger, dir, e.Name(), cutoff)
		}
	}
}

// gcMaybeRemoveSpan removes a span directory if its UUIDv7 timestamp is older
// than the cutoff. Handles both final and .tmp directories.
func gcMaybeRemoveSpan(logger *slog.Logger, parent, name string, cutoff time.Time) {
	// Entries are UUIDv7 with an optional `.tmp` suffix if in progress
	// that said it's fine to delete very old in progress entries.
	uuidStr := strings.TrimSuffix(name, ".tmp")
	spanID, err := uuid.Parse(uuidStr)
	if err != nil {
		return
	}
	if spanID.Version() != 7 {
		return
	}

	// Determine whether this entry is too new to remove.
	sec, nsec := spanID.Time().UnixTime()
	ts := time.Unix(sec, nsec)
	if !ts.Before(cutoff) {
		return
	}

	// Remove the directory entry.
	spanPath := filepath.Join(parent, name)
	if err := os.RemoveAll(spanPath); err != nil {
		logger.Warn("failed to remove span", slog.String("path", spanPath), slog.Any("err", err))
	}
}
