// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/cmd/internal/plugincommand"
	"github.com/bassosimone/sonda/internal/config"
	"github.com/bassosimone/sonda/internal/spoolclient"
	"github.com/bassosimone/sonda/internal/spoolfs"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
)

func main() {
	plugincommand.Main(0, gcMain)
}

// gcMain is the main function of the `sonda spool-gc` subcommand.
func gcMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Set the command defaults.
	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))
	presets := config.Defaults()
	configErr := config.ReadDefaultsInto(env, presets)
	var (
		maxAge      = time.Duration(presets.Spool.GC.MaxAge)
		spoolDir    = config.SpoolDir
		spoolSocket = config.SpoolSocketPath
	)

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda-spool-gc", vflag.ExitOnError)

	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.UsageStdout

	upr := vflag.NewDefaultUsagePrinter()
	fset.UsagePrinter = upr
	upr.AddDescription(
		"Remove span directories whose UUIDv7 timestamp is older than " +
			"`--max-age`, including incomplete `.tmp` ones, for every data type " +
			"directory under `--spool-dir` (e.g., `<spool-dir>/ptnop`). Also " +
			"remove the sharding directories left empty. Then send a GC " +
			"request to the spool service at `--spool-socket`.")

	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.DurationVar(&maxAge, 0, "max-age", "Remove spans older than `DURATION`.",
		"Default: `@DEFAULT_VALUE@`.")

	fset.StringVar(&spoolSocket, 0, "spool-socket",
		"Unix domain socket `PATH` of the `sonda-serve-spool` server.",
		"Default: `@DEFAULT_VALUE@`.")

	fset.StringVar(&spoolDir, 0, "spool-dir",
		"Top-level spool `DIR` containing the data type directories.",
		"Default: `@DEFAULT_VALUE@`.")

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Defer reporting config errors after flag parsing to honor `-h/--help`.
	if configErr != nil {
		logger.Error("config.ReadDefaultsInto", slog.Any("err", configErr))
		env.Exit(1)
	}

	// Open the root spool directory using the spool API.
	spoolRootDir, err := spoolfs.NewRootDir(env, spoolDir)
	if err != nil {
		logger.Error("spoolfs.NewRootDir", slog.Any("err", err))
		env.Exit(1)
	}

	// Walk the spool structure: spoolDir/<dataType>/XXXX/X/X/<spanID>.
	cutoff := time.Now().Add(-maxAge)
	spoolRootDir.GarbageCollect(ctx, cutoff, logger)

	// Ask the spool service to run its in-memory GC cycle.
	spoolRemoteDir, err := spoolclient.NewRootDir(env, spoolSocket)
	if err != nil {
		logger.Error("spoolclient.NewRootDir", slog.Any("err", err))
		env.Exit(1)
	}
	spoolRemoteDir.GarbageCollect(ctx, cutoff, logger)
	return nil
}
