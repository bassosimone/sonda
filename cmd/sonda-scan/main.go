// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/cmd/internal/plugincommand"
	"github.com/bassosimone/sonda/internal/config"
	"github.com/bassosimone/sonda/internal/ptnopclient"
	"github.com/bassosimone/sonda/internal/scan/actions"
	"github.com/bassosimone/sonda/internal/spoolfs"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/sonda/internal/triggers"
	"github.com/bassosimone/vflag"
	"github.com/google/uuid"
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
		fail         = false
		ptnopSocket  = config.PtnopSocketPath
		runDir       = config.RunDir
		spoolDir     = config.SpoolDir
		workflowFile = ""
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

	fset.StringVar(&spoolDir, 0, "spool-dir",
		"Top-level spool `DIR` containing the data type directories.",
		"Default: `@DEFAULT_VALUE@`.")

	fset.StringVar(&workflowFile, 0, "workflow-file", "Load steps from `FILE` (required).")

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Emit structured logs to stderr.
	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))

	// Provide useful output when no flags were specified.
	if len(args) <= 0 {
		fset.PrintUsageString(fset.Stdout)
		env.Exit(0)
	}

	// Determine which steps to execute.
	if workflowFile == "" {
		logger.Error("no `--workflow-file` specified; nothing to do.")
		env.Exit(2)
	}
	spec, err := actions.LoadWorkflowSpec(env, workflowFile)
	if err != nil {
		logger.Error("loading workflow", slog.Any("err", err))
		env.Exit(2)
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
	runner := actions.NewWorkflowRunner(spoolRootDir)

	// Run all the steps in the spec.
	err = runner.RunSpec(ctx, conn, logger, spec)

	// Execute triggers for the created spans.
	scanMustWriteTriggers(env, logger, runDir, runner, spec.Triggers)

	// Deferred exit from running the spec.
	if err != nil && fail {
		env.Exit(1)
	}
	return nil
}

// scanMustWriteTriggers writes triggers if necessary and exits on failure.
func scanMustWriteTriggers(
	env *testable.Environ, logger *slog.Logger, runDir string,
	runner *actions.WorkflowRunner, triggerNames []string) {
	// 1. Determine whether we actually need to write triggers.
	if len(triggerNames) <= 0 {
		return
	}
	created := runner.CreatedSpans()
	if len(created) <= 0 {
		return
	}

	// 2. Determine the work unit ID.
	workUnitID := uuid.Must(uuid.NewV7()).String()

	// 3. Write the work unit inside the `$runDir/scan` directory.
	workUnitPath := filepath.Join(runDir, "scan", workUnitID+".jsonl")
	filep, err := env.OpenFile(workUnitPath, os.O_CREATE|os.O_WRONLY, 0640)
	if err != nil {
		logger.Error("env.OpenFile", slog.Any("err", err))
		env.Exit(1)
	}
	for _, entry := range created {
		data := runtimex.PanicOnError1(json.Marshal(entry))
		data = append(data, '\n')
		if _, err := filep.Write(data); err != nil {
			logger.Error("filep.Write", slog.Any("err", err))
			env.Exit(1)
		}
	}
	if err := filep.Close(); err != nil {
		logger.Error("filep.Close", slog.Any("err", err))
		env.Exit(1)
	}

	// 4. Create hard links to trigger the pipelines.
	for _, tname := range triggerNames {
		if !triggers.ValidName[tname] {
			logger.Warn(
				"trigger.ValidName",
				slog.String("err", "invalid trigger name"),
				slog.String("name", tname),
			)
			continue
		}
		tdirpath := triggers.Directory(runDir, tname)
		tfilepath := filepath.Join(tdirpath, workUnitID+".jsonl")
		if err := env.Link(workUnitPath, tfilepath); err != nil {
			logger.Error("env.Link", slog.Any("err", err))
			env.Exit(1)
		}
	}

	// 5. Unlink the `scan` file.
	if err := env.Remove(workUnitPath); err != nil {
		logger.Error("env.Remove", slog.Any("err", err))
		env.Exit(1)
	}
}
