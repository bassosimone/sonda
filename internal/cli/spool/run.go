// SPDX-License-Identifier: GPL-3.0-or-later

package spool

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bassosimone/closepool"
	"github.com/bassosimone/nop"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/paths"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
)

// runMain is the main function of the `sonda-spool run` subcommand.
func runMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)
	logger := slog.New(slog.NewJSONHandler(env.Stdout, nil))

	// Set command defaults.
	var (
		spanID   = nop.NewSpanID()
		spoolDir = env.Getenv("SONDA_SPOOL_DIR")
		timeout  = 5 * time.Minute
	)

	// Parse command line flags
	fset := vflag.NewFlagSet("sonda-spool run", vflag.ExitOnError)
	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.UsageStdout
	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.StringVar(&spanID, 0, "span-id", "Use `ID` instead of generating a random one.")
	fset.StringVar(&spoolDir, 0, "spool-dir", "Use `DIR` instead of `$SONDA_SPOOL_DIR`.")
	fset.DurationVar(&timeout, 0, "timeout", "Use `DURATION` instead of `@DEFAULT_VALUE@`.")
	fset.SetMinMaxPositionalArgs(1, math.MaxInt)
	fset.DisablePermute = true               // make the `--` optional
	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Refuse to guess the spool directory.
	if spoolDir == "" {
		err := errors.New("neither --spool-dir nor SONDA_SPOOL_DIR is set")
		fset.PrintUsageError(env.Stderr, err)
		env.Exit(2)
	}

	// Remaining args after "--" are the command to execute.
	cmdArgs := fset.Args()
	runtimex.Assert(len(cmdArgs) > 0)

	// Build the spool directory path.
	spanDir := paths.SpanDir(spoolDir, spanID)
	tmpDir := paths.SpanDirTmp(spoolDir, spanID)

	// Expand @SONDA_SPAN_DIR@ in the command arguments so that inner
	// commands can reference the span directory for auxiliary files.
	for idx, arg := range cmdArgs {
		cmdArgs[idx] = strings.ReplaceAll(arg, "@SONDA_SPAN_DIR@", tmpDir)
	}

	// Create the temporary spool directory.
	if err := env.MkdirAll(tmpDir, 0750); err != nil {
		logger.Error("failed to create spool directory", slog.Any("err", err))
		env.Exit(1)
	}

	logger.Info(
		"spoolRunMkdirTmp",
		slog.String("spoolDir", spoolDir),
		slog.String("spanId", spanID),
		slog.String("spanDirTmp", paths.SpanDirTmpRelative(spanID)),
	)

	// Record the command that will be executed.
	argvData, err := json.Marshal(cmdArgs)
	if err != nil {
		logger.Error("failed to marshal argv", slog.Any("err", err))
		env.Exit(1)
	}
	argvData = append(argvData, '\n')
	if err := env.WriteFile(paths.SpanArgvJSON(tmpDir), argvData, 0640); err != nil {
		logger.Error("failed to write argv.json", slog.Any("err", err))
		env.Exit(1)
	}

	// Open stdout and stderr files in the spool directory.
	closers := &closepool.Pool{}
	defer closers.Close() // idempotent

	openFlags := os.O_CREATE | os.O_TRUNC | os.O_WRONLY
	stdoutPath := paths.SpanStdout(tmpDir)
	stdoutFile, err := env.OpenFile(stdoutPath, openFlags, 0640)
	if err != nil {
		logger.Error("failed to open stdout", slog.Any("err", err))
		env.Exit(1)
	}
	closers.Add(stdoutFile)

	stderrPath := paths.SpanStderr(tmpDir)
	stderrFile, err := env.OpenFile(stderrPath, openFlags, 0640)
	if err != nil {
		logger.Error("failed to open stderr", slog.Any("err", err))
		env.Exit(1)
	}
	closers.Add(stderrFile)

	// Build the environment with timeout context.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	env = testable.WithEnvOverrides(env, "SONDA_SPAN_ID="+spanID) // clones env
	env.Stdin = strings.NewReader("")
	env.Stdout = stdoutFile
	env.Stderr = stderrFile
	ctx = testable.WithEnviron(ctx, env)

	// Run the command and record the exit code.
	exitCode := 0
	if err := env.ReExec(ctx, cmdArgs); err != nil {
		exitCode = env.AsExitCode(err)
	}

	// Make sure we successfully closed both stdout.txt and stderr.txt.
	if err := closers.Close(); err != nil {
		logger.Error("failed to close output files", slog.Any("err", err))
		env.Exit(1)
	}

	// Write the exit code to the spool directory.
	exitCodeData := []byte(strconv.Itoa(exitCode) + "\n")
	if err := env.WriteFile(paths.SpanExitCode(tmpDir), exitCodeData, 0640); err != nil {
		logger.Error("failed to write exit code", slog.Any("err", err))
		env.Exit(1)
	}

	// Atomically rename the temporary directory to the final path.
	if err := env.Rename(tmpDir, spanDir); err != nil {
		logger.Error("failed to finalize span directory", slog.Any("err", err))
		env.Exit(1)
	}

	logger.Info(
		"spoolRunRenameDir",
		slog.String("spoolDir", spoolDir),
		slog.String("spanId", spanID),
		slog.String("spanDirTmp", paths.SpanDirTmpRelative(spanID)),
		slog.String("spanDir", paths.SpanDirRelative(spanID)),
	)

	return nil
}
