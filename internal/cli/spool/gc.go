// SPDX-License-Identifier: GPL-3.0-or-later

package spool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
	"github.com/google/uuid"
)

// gcMain is the main function of the `sonda spool gc` subcommand.
func gcMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.Env

	// Set command defaults.
	var (
		maxAge   = 6 * time.Hour
		spoolDir = "."
	)

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda spool gc", vflag.ExitOnError)
	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.Stdout
	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.DurationVar(&maxAge, 0, "max-age", "Remove spans older than `DURATION`.")
	fset.StringVar(&spoolDir, 0, "spool-dir", "Use `DIR` instead of `@DEFAULT_VALUE@`.")
	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Compute the cutoff time.
	cutoff := time.Now().Add(-maxAge)

	// Walk the spool sharding structure: spoolDir/XXXX/X/X/<spanID>.
	gcWalkDir(env, spoolDir, cutoff, 3)
	return nil
}

// gcWalkDir walks the spool sharding tree recursively. At depth > 0,
// it descends into subdirectories and removes empty ones. At depth 0,
// it processes span directories.
func gcWalkDir(env *testable.Environ, dir string, cutoff time.Time, depth int) {
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
			gcWalkDir(env, child, cutoff, depth-1)
			os.Remove(child)
		} else {
			gcMaybeRemoveSpan(env, dir, e.Name(), cutoff)
		}
	}
}

// gcMaybeRemoveSpan removes a span directory if its UUIDv7 timestamp is older
// than the cutoff. Handles both final and .tmp directories.
func gcMaybeRemoveSpan(env *testable.Environ, parent, name string, cutoff time.Time) {
	// Entries are UUIDv7 with an optional `.tmp` prefix if in progress
	// that said it's fine to delete very old in progress entries.
	uuidStr := strings.TrimSuffix(name, ".tmp")
	id, err := uuid.Parse(uuidStr)
	if err != nil {
		return
	}
	if id.Version() != 7 {
		return
	}

	// Determine whether this entry is too new to remove.
	sec, nsec := id.Time().UnixTime()
	ts := time.Unix(sec, nsec)
	if !ts.Before(cutoff) {
		return
	}

	// Remove the directory entry.
	spanPath := filepath.Join(parent, name)
	if err := os.RemoveAll(spanPath); err != nil {
		fmt.Fprintf(env.Stderr, "sonda spool gc: %s\n", err)
	}
}
