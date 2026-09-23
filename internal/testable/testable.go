// SPDX-License-Identifier: GPL-3.0-or-later

// Package testable contains code to make sonda testable.
package testable

import (
	"context"
	"io"
	"log"
	"net"
	"os"
	"os/exec"

	"github.com/bassosimone/deferexit"
	"github.com/bassosimone/runtimex"
)

// Dialer abstracts network dialing.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// Environ abstracts away side effects (I/O, exit) so that commands
// can be tested without real I/O or process termination.
type Environ struct {
	Args             []string
	Dialer           Dialer
	Environ          func() []string
	Executable       func() (string, error)
	Exit             func(code int)
	Getenv           func(key string) string
	LogFatalOnError0 func(err error)
	MkdirAll         func(path string, perm os.FileMode) error
	OpenFile         func(name string, flag int, perm os.FileMode) (*os.File, error)
	Rename           func(oldpath, newpath string) error
	RunCommand       func(cmd *exec.Cmd) error
	Stdin            io.Reader
	Stderr           io.Writer
	WriteFile        func(name string, data []byte, perm os.FileMode) error

	// Stdout carries a command's output and UsageStdout carries its
	// usage, help, and version text. Both are os.Stdout by default.
	// Keeping them separate lets a caller hosting commands in-process
	// redirect the usage text, so that it never lands in the stream
	// where a program expects structured logs.
	Stdout      io.Writer
	UsageStdout io.Writer
}

// NewEnvironOS returns an [*Environ] wired to real OS operations.
func NewEnvironOS() *Environ {
	return &Environ{
		Args:       os.Args,
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
		OpenFile: os.OpenFile,
		Rename:   os.Rename,
		RunCommand: func(cmd *exec.Cmd) error {
			return cmd.Run()
		},
		Stdin:       os.Stdin,
		Stderr:      os.Stderr,
		WriteFile:   os.WriteFile,
		Stdout:      os.Stdout,
		UsageStdout: os.Stdout,
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
