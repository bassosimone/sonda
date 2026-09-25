// SPDX-License-Identifier: GPL-3.0-or-later

// Package reexec implements `sonda` subcommands reexecution.
package reexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/bassosimone/sonda/internal/testable"
)

// Subcommand invokes `sonda` with the given args thus executing a subcommand.
//
// When the context is cancelled, we first send SIGINT and later SIGKILL after
// a five seconds grace time. Execution goes through the `sonda` launcher, which
// uses `signal.NotifyContext` via `vclip.RootCommand`.
//
// By overriding `env.Executable` you can also use this functionality to
// execute a specific `sonda` plugin rather than `sonda` itself.
func Subcommand(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Obtain the executable path.
	exePath, err := env.Executable()
	if err != nil {
		return err
	}

	// Prepare for running the sonda subcommand using the `env` configuration.
	cmd := exec.CommandContext(ctx, exePath, args...)
	cmd.Env = env.Environ()
	cmd.Stdin = env.Stdin
	cmd.Stdout = env.Stdout
	cmd.Stderr = env.Stderr

	// On context cancellation, send SIGINT first; escalate to
	// SIGKILL after the wait delay.
	cmd.Cancel = func() error {
		return env.SignalProcess(cmd.Process, os.Interrupt)
	}
	cmd.WaitDelay = 5 * time.Second

	// Run the child process until termination.
	return env.RunCommand(cmd)
}

// AsExitCode maps the error returned by [Subcommand] to an exit code.
func AsExitCode(err error) int {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		code := exitErr.ExitCode()
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			code = 128 + int(ws.Signal())
		}
		return code
	}
	if err != nil {
		return 1
	}
	return 0
}
