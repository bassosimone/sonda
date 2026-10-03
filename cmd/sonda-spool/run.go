// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bassosimone/closepool"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/ptnoppaths"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
	"github.com/google/uuid"
)

// newSpanID returns a new span ID, which is a UUIDv7 in canonical form.
//
// We generate the span ID here rather than accepting it from the caller
// so that we never need to validate it: `gc` and the ETL plugins only
// consider UUIDv7 entries, and a malformed ID could escape `--spool-dir`.
func newSpanID() string {
	return uuid.Must(uuid.NewV7()).String()
}

// runResult is the JSON object that `sonda-spool run` writes to its
// stdout after the span directory has been atomically renamed.
type runResult struct {
	// SpanID is the generated span ID.
	SpanID string `json:"spanId"`

	// SpanDir is the final span directory path, derived from `--spool-dir`
	// and thus relative when `--spool-dir` is relative.
	SpanDir string `json:"spanDir"`
}

// runMain is the main function of the `sonda-spool run` subcommand.
func runMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)
	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))

	// Set command defaults.
	var (
		spoolDir = "."
		timeout  = 5 * time.Minute
	)

	// Parse command line flags
	fset := vflag.NewFlagSet("sonda-spool run", vflag.ExitOnError)
	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.UsageStdout
	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.StringVar(&spoolDir, 0, "spool-dir", "Use `DIR` instead of `@DEFAULT_VALUE@`.")
	fset.DurationVar(&timeout, 0, "timeout", "Use `DURATION` instead of `@DEFAULT_VALUE@`.")
	fset.SetMinMaxPositionalArgs(1, math.MaxInt)
	fset.DisablePermute = true               // make the `--` optional
	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Remaining args after "--" are the command to execute.
	cmdArgs := fset.Args()
	runtimex.Assert(len(cmdArgs) > 0)

	// Save our stdout before overriding it for the child, since we use
	// it at the end to tell the caller where we wrote the span.
	stdout := env.Stdout

	// Generate the span ID and build the spool directory path.
	spanID := newSpanID()
	spanDir := ptnoppaths.SpanDir(spoolDir, spanID)
	tmpDir := ptnoppaths.SpanDirTmp(spoolDir, spanID)

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

	// Record the command that will be executed.
	argvData, err := json.Marshal(cmdArgs)
	if err != nil {
		logger.Error("failed to marshal argv", slog.Any("err", err))
		env.Exit(1)
	}
	argvData = append(argvData, '\n')
	if err := env.WriteFile(ptnoppaths.SpanArgvJSON(tmpDir), argvData, 0640); err != nil {
		logger.Error("failed to write argv.json", slog.Any("err", err))
		env.Exit(1)
	}

	// Open stdout and stderr files in the spool directory.
	closers := &closepool.Pool{}
	defer closers.Close() // idempotent

	openFlags := os.O_CREATE | os.O_TRUNC | os.O_WRONLY
	stdoutPath := ptnoppaths.SpanStdout(tmpDir)
	stdoutFile, err := env.OpenFile(stdoutPath, openFlags, 0640)
	if err != nil {
		logger.Error("failed to open stdout", slog.Any("err", err))
		env.Exit(1)
	}
	closers.Add(stdoutFile)

	stderrPath := ptnoppaths.SpanStderr(tmpDir)
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
	if err := env.WriteFile(ptnoppaths.SpanExitCode(tmpDir), exitCodeData, 0640); err != nil {
		logger.Error("failed to write exit code", slog.Any("err", err))
		env.Exit(1)
	}

	// Atomically rename the temporary directory to the final path.
	if err := env.Rename(tmpDir, spanDir); err != nil {
		logger.Error("failed to finalize span directory", slog.Any("err", err))
		env.Exit(1)
	}

	// Tell the caller where we wrote the span.
	//
	// Marshalling cannot fail: runResult only contains strings.
	resultData := runtimex.PanicOnError1(json.Marshal(&runResult{SpanID: spanID, SpanDir: spanDir}))
	resultData = append(resultData, '\n')
	if _, err := stdout.Write(resultData); err != nil {
		logger.Error("failed to write result", slog.Any("err", err))
		env.Exit(1)
	}

	return nil
}
