// SPDX-License-Identifier: GPL-3.0-or-later

// Package reexec implements `sonda` subcommands reexecution.
package reexec

import (
	"context"
	"errors"
	"fmt"
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
	//
	// Log a message on failure to populate the subcommand stderr.txt.
	exePath, err := env.Executable()
	if err != nil {
		fmt.Fprintf(env.Stderr, "sonda: reexec: %s\n", err.Error())
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
	//
	// Log a message on failure to populate the subcommand stderr.txt.
	err = env.RunCommand(cmd)
	if AsExitCode(err) == 127 {
		fmt.Fprintf(env.Stderr, "sonda: reexec: %s\n", err.Error())
		// fallthrough
	}
	return err
}

// AsExitCode maps the error returned by [Subcommand] to an exit code.
//
// Conventions:
//
//  1. Return the process exit code if it exited normally
//
//  2. Return 128 + the signal number if it was terminated by a signal
//
//  3. Return 127 if executing the process fails
//
// We use these conventions to align to bash. Note that bash also has 126 when the
// command exists but is not executable. We take a (portable) shortcut and always use
// 127 with this reasoning: if it is not executable, it is not a command.
func AsExitCode(err error) int {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		code := exitErr.ExitCode()
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			code = 128 + int(ws.Signal())
		}
		return code
	}
	if pathErr, ok := errors.AsType[*os.PathError](err); ok && pathErr.Op == "fork/exec" {
		return 127
	}
	if err != nil {
		return 1
	}
	return 0
}

// WithReExecFeature returns a copy of [context.Context] bound to a [*testable.Environ]
// patched so that `sonda` can re-execute itself using `SONDA_COMMAND`.
func WithReExecFeature(ctx context.Context) context.Context {
	env := testable.ContextEnviron(ctx).Clone()
	env.ReExec = Subcommand
	env.AsExitCode = AsExitCode
	getenv := env.Getenv
	env.Executable = func() (string, error) {
		value := getenv("SONDA_COMMAND")
		if value == "" {
			err := &os.PathError{ // cause exit code 127
				Op:   "fork/exec",
				Path: "sonda",
				Err:  errors.New("the SONDA_COMMAND environment variable is not set"),
			}
			return "", err
		}
		return value, nil
	}
	return testable.WithEnviron(ctx, env)
}
