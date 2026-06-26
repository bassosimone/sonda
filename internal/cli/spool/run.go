// SPDX-License-Identifier: GPL-3.0-or-later

package spool

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"os/exec"
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

// runMain is the main function of the `sonda spool run` subcommand.
func runMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.Env
	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))

	// Set command defaults.
	var (
		spanID   = nop.NewSpanID()
		spoolDir = "."
		timeout  = 5 * time.Minute
	)

	// Parse command line flags
	fset := vflag.NewFlagSet("sonda spool run", vflag.ExitOnError)
	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.Stdout
	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.StringVar(&spanID, 0, "span-id", "Use `ID` instead of generating a random one.")
	fset.StringVar(&spoolDir, 0, "spool-dir", "Use `DIR` instead of `@DEFAULT_VALUE@`.")
	fset.DurationVar(&timeout, 0, "timeout", "Use `DURATION` instead of `@DEFAULT_VALUE@`.")
	fset.SetMinMaxPositionalArgs(1, math.MaxInt)
	fset.DisablePermute = true               // make the `--` optional
	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Remaining args after "--" are the command to execute.
	cmdArgs := fset.Args()
	runtimex.Assert(len(cmdArgs) > 0)

	// Build the spool directory path.
	spanDir := paths.SpanDir(spoolDir, spanID)
	tmpDir := paths.SpanDirTmp(spoolDir, spanID)

	// Expand @SONDA_SPAN_DIR@ in the command arguments so that inner
	// commands can reference the span directory for auxiliary files.
	for i, arg := range cmdArgs {
		cmdArgs[i] = strings.ReplaceAll(arg, "@SONDA_SPAN_DIR@", tmpDir)
	}

	// Create the temporary spool directory.
	if err := env.MkdirAll(tmpDir, 0750); err != nil {
		logger.Error("failed to create spool directory", slog.Any("err", err))
		env.Exit(1)
	}

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

	// Build the command with timeout context.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Env = append(env.Environ(), "SONDA_SPAN_ID="+spanID)
	cmd.Stdin = nil
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile

	// On context cancellation, send SIGINT first; escalate to
	// SIGKILL after the wait delay.
	cmd.Cancel = func() error {
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = 5 * time.Second

	// Run the command and record the exit code.
	exitCode := 0
	if err := env.RunCommand(cmd); err != nil {
		exitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
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

	return nil
}
