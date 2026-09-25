// SPDX-License-Identifier: GPL-3.0-or-later

// Package testable contains code to make sonda testable.
package testable

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/bassosimone/deferexit"
	"github.com/bassosimone/runtimex"
)

// Dialer abstracts network dialing.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// File is an abstract [*os.File] as returned by [os.OpenFile].
//
// The type is wide enough to accommodate both readers and writers.
type File = io.ReadWriteCloser

// Environ abstracts away side effects (I/O, exit) so that commands
// can be tested without real I/O or process termination.
type Environ struct {
	Args             []string
	AsExitCode       func(err error) int
	Dialer           Dialer
	Environ          func() []string
	Executable       func() (string, error)
	Exit             func(code int)
	Getenv           func(key string) string
	LogFatalOnError0 func(err error)
	MkdirAll         func(path string, perm os.FileMode) error
	Rename           func(oldpath, newpath string) error
	RunCommand       func(cmd *exec.Cmd) error
	SignalProcess    func(proc *os.Process, sig os.Signal) error
	Stdin            io.Reader
	Stderr           io.Writer
	WriteFile        func(name string, data []byte, perm os.FileMode) error

	// OpenFile is like [os.OpenFile] but abstract in the returned file type, to accommodate
	// testing and hosting in a library, where the [File] could be memory or a pipe.
	OpenFile func(name string, flag int, perm os.FileMode) (File, error)

	// ReExec allows to re-execute `sonda` with the given command
	// line arguments. By default, this function does not allow
	// re-execution of sonda. The `main.go` should configure this
	// functionality when/if it wants to enable it.
	ReExec func(ctx context.Context, args []string) error

	// Stdout carries a command's output and UsageStdout carries its
	// usage, help, and version text. Both are os.Stdout by default.
	// Keeping them separate lets a caller hosting commands in-process
	// redirect the usage text, so that it never lands in the stream
	// where a program expects structured logs.
	Stdout      io.Writer
	UsageStdout io.Writer
}

// ErrNoReExec indicates that re-execution is not enabled.
var ErrNoReExec = errors.New("sonda: re-execution not enabled")

// NewEnvironOS returns an [*Environ] wired to real OS operations.
func NewEnvironOS() *Environ {
	return &Environ{
		Args: os.Args,
		AsExitCode: func(err error) int {
			if err != nil {
				return 1
			}
			return 0
		},
		Dialer:     newDialer(),
		Environ:    os.Environ,
		Executable: os.Executable,
		Exit:       deferexit.Panic,
		Getenv:     os.Getenv,
		LogFatalOnError0: func(err error) {
			if err != nil {
				log.Print(err)
				deferexit.Panic(1)
			}
		},
		MkdirAll: os.MkdirAll,
		Rename:   os.Rename,
		RunCommand: func(cmd *exec.Cmd) error {
			return cmd.Run()
		},
		SignalProcess: func(proc *os.Process, sig os.Signal) error {
			return proc.Signal(sig)
		},
		Stdin:     os.Stdin,
		Stderr:    os.Stderr,
		WriteFile: os.WriteFile,
		OpenFile: func(name string, flag int, perm os.FileMode) (File, error) {
			return os.OpenFile(name, flag, perm)
		},
		ReExec: func(ctx context.Context, args []string) error {
			return ErrNoReExec
		},
		Stdout:      os.Stdout,
		UsageStdout: os.Stdout,
	}
}

// Clone returns a copy of [*Environ] that is safe to mutate into its obviously
// mutable elements: slices. The other fields are returned verbatim.
func (e *Environ) Clone() *Environ {
	return &Environ{
		Args:             append([]string{}, e.Args...),
		AsExitCode:       e.AsExitCode,
		Dialer:           e.Dialer,
		Environ:          e.Environ,
		Executable:       e.Executable,
		Exit:             e.Exit,
		Getenv:           e.Getenv,
		LogFatalOnError0: e.LogFatalOnError0,
		MkdirAll:         e.MkdirAll,
		Rename:           e.Rename,
		RunCommand:       e.RunCommand,
		SignalProcess:    e.SignalProcess,
		Stdin:            e.Stdin,
		Stderr:           e.Stderr,
		WriteFile:        e.WriteFile,
		OpenFile:         e.OpenFile,
		ReExec:           e.ReExec,
		Stdout:           e.Stdout,
		UsageStdout:      e.UsageStdout,
	}
}

func newDialer() *net.Dialer {
	d := &net.Dialer{}
	d.SetMultipathTCP(false)
	return d
}

// Env is the global [*Environ].
var Env = NewEnvironOS()

// contextKey is the key used for binding a [*Environ] to a context.
type contextKey struct{}

// WithEnviron returns a new [context.Context] bound to the given [*Environ].
//
// This method panics if the `ctx` is nil or `env` is nil.
func WithEnviron(ctx context.Context, env *Environ) context.Context {
	runtimex.Assert(ctx != nil && env != nil)
	return context.WithValue(ctx, contextKey{}, env)
}

// ContextEnviron returns the [*Environ] previously associated with
// the [context.Context] using [WithEnviron] or the default [*Environ]
// when no previous association was created with the context.
func ContextEnviron(ctx context.Context) *Environ {
	env, _ := ctx.Value(contextKey{}).(*Environ)
	if env == nil {
		env = Env
	}
	return env
}

// WithEnvOverrides returns a copy of `env` overriding the given environment variables.
func WithEnvOverrides(env *Environ, overrides ...string) *Environ {
	// 1. collect unique environment variables preferring overrides to originals.
	overrides = append(slices.Clone(env.Environ()), overrides...)
	uniq := make(map[string]string)
	for _, entry := range overrides {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		uniq[key] = value
	}

	// 2. build a sorted list of environment variables.
	var sorted []string
	for _, key := range slices.Sorted(maps.Keys(uniq)) {
		sorted = append(sorted, fmt.Sprintf("%s=%s", key, uniq[key]))
	}

	// 3. change environment accessing funcs.
	env = env.Clone()
	env.Getenv = func(key string) string {
		return uniq[key]
	}
	env.Environ = func() []string {
		return sorted
	}

	return env
}
