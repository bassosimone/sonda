// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/cmd/internal/plugincommand"
	"github.com/bassosimone/sonda/internal/config"
	"github.com/bassosimone/sonda/internal/ptnopclient"
	"github.com/bassosimone/sonda/internal/scan/actions"
	"github.com/bassosimone/sonda/internal/scan/scheduler"
	"github.com/bassosimone/sonda/internal/spoolfs"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/sonda/internal/triggers"
	"github.com/bassosimone/vflag"
)

func main() {
	plugincommand.Main(0, scanMain)
}

// scanMain is the main function of the `sonda scan` subcommand.
func scanMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Set the command defaults.
	var (
		fail           = false
		ptnopSocket    = config.PtnopSocketPath
		runDir         = config.RunDir
		schedStateFile = config.ScanSchedulerState
		spoolDir       = config.SpoolDir
		workflowFiles  = []string{}
	)

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda-scan", vflag.ExitOnError)

	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.UsageStdout

	upr := vflag.NewDefaultUsagePrinter()
	fset.UsagePrinter = upr
	upr.AddDescription(
		"Run the steps listed in the `--workflow-file` YAML file, in order. " +
			"Each step sends a measurement request to the `sonda-serve-ptnop` server " +
			"listening at `--ptnop-socket`, which stores the results in the spool. " +
			"A failed step does not stop the scan unless `--fail` is set.")

	fset.AutoHelp('h', "help", "Show this help message and exit.")

	fset.BoolVar(&fail, 0, "fail", "Exit with error if any failure occurs.")

	fset.StringVar(&ptnopSocket, 0, "ptnop-socket",
		"Unix domain socket `PATH` of the `sonda-serve-ptnop` server.",
		"Default: `@DEFAULT_VALUE@`.")

	fset.StringVar(&runDir, 0, "run-dir",
		"Directory `PATH` in which to write trigger files.",
		"Default: `@DEFAULT_VALUE@`.")

	fset.StringVar(&schedStateFile, 0, "scheduler-state",
		"File `PATH` in which to write the scheduler state.",
		"Default: `@DEFAULT_VALUE@`.")

	fset.StringVar(&spoolDir, 0, "spool-dir",
		"Top-level spool `DIR` containing the data type directories.",
		"Default: `@DEFAULT_VALUE@`.")

	fset.StringSliceVar(
		&workflowFiles, 0, "workflow-file",
		"Load the workflow steps from `FILE` (required).",
		"Can be repeated to run multiple workflows.")

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Emit structured logs to stderr.
	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))

	// Provide useful output when no flags were specified.
	if len(args) <= 0 {
		fset.PrintUsageString(fset.Stdout)
		env.Exit(0)
	}

	// Determine which steps to execute. Load all specs in advance so that
	// we can exit 2 and inform the user about issues.
	var specs []*scheduler.WorkflowSpec
	if len(workflowFiles) <= 0 {
		logger.Error("no `--workflow-file` specified; nothing to do.")
		env.Exit(2)
	}

	state, err := scheduler.LoadState(env, schedStateFile)
	switch {
	case err != nil && state == nil:
		logger.Error(
			"opening scheduler state",
			slog.Any("err", err),
			slog.String("path", schedStateFile),
		)
		env.Exit(1)

	case err != nil && !errors.Is(err, fs.ErrNotExist):
		logger.Warn(
			"opening scheduler state",
			slog.Any("err", err),
			slog.String("path", schedStateFile),
		)
		// continue like we succeeded
	}

	for _, entry := range workflowFiles {
		spec, err := state.LoadWorkflowSpec(env, entry)
		if err != nil {
			logger.Error(
				"loading workflow spec",
				slog.Any("err", err),
				slog.String("path", entry),
			)
			env.Exit(2)
		}
		specs = append(specs, spec)
	}

	// Connect to the ptnop server, using a single connection for all the steps.
	//
	// We connect after loading the workflow, so `scan` fails early when the server
	// is not running, but still prints the usage or workflow errors without it.
	conn, err := ptnopclient.Dial(ctx, env, ptnopSocket)
	if err != nil {
		logger.Error("connecting to the ptnop server", slog.Any("err", err))
		env.Exit(1)
	}
	defer conn.Close()

	// Construct shared dependencies.
	spoolRootDir, err := spoolfs.NewRootDir(env, spoolDir)
	if err != nil {
		logger.Error("spoolfs.NewRootDir", slog.Any("err", err))
		env.Exit(1)
	}

	// Loop over all workflows syncing the scheduler as we proceed
	var errv []error
	for _, spec := range specs {
		if !state.ShouldRun(spec) {
			continue
		}
		if err := state.WillStart(env, spec); err != nil {
			logger.Error("state.WillStart", slog.Any("err", err))
			env.Exit(1)
		}

		// A single runner per loop to collect and emit triggers per workflow
		// such that we avoid emitting all triggers at once.
		runner := actions.NewWorkflowRunner(spoolRootDir)

		// Defer acting upon the errors until we have synced the state.
		runSpecErr := runner.RunSpec(ctx, conn, logger, spec.Data)
		triggersErr := triggers.Write(env, logger, runDir, runner, spec.Data.Triggers)

		if err := state.DidRun(env, spec); err != nil {
			logger.Error("state.DidRun", slog.Any("err", err))
			env.Exit(1)
		}

		// Finally account for nonfatal errors
		if err := runSpecErr; err != nil {
			// warning already printed by runner.RunSpec
			errv = append(errv, err)
		}
		if err := triggersErr; err != nil {
			logger.Warn("triggers.Write", slog.Any("err", err))
			errv = append(errv, err)
		}
	}

	// Deferred exit from running the spec if there were errors.
	if len(errv) > 0 && fail {
		env.Exit(1)
	}
	return nil
}
