// SPDX-License-Identifier: GPL-3.0-or-later

// Package testable contains code to make sonda testable.
package testable

import (
	"io"
	"os"
	"os/exec"

	"github.com/bassosimone/deferexit"
)

// Environ abstracts away side effects (I/O, exit) so that commands
// can be tested without real I/O or process termination.
type Environ struct {
	Args       []string
	Environ    func() []string
	Exit       func(code int)
	Getenv     func(key string) string
	MkdirAll   func(path string, perm os.FileMode) error
	OpenFile   func(name string, flag int, perm os.FileMode) (*os.File, error)
	Rename     func(oldpath, newpath string) error
	RunCommand func(cmd *exec.Cmd) error
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	WriteFile  func(name string, data []byte, perm os.FileMode) error
}

// NewEnvironOS returns an [*Environ] wired to real OS operations.
func NewEnvironOS() *Environ {
	return &Environ{
		Args:     os.Args,
		Environ:  os.Environ,
		Exit:     deferexit.Panic,
		Getenv:   os.Getenv,
		MkdirAll: os.MkdirAll,
		OpenFile: os.OpenFile,
		Rename:   os.Rename,
		RunCommand: func(cmd *exec.Cmd) error {
			return cmd.Run()
		},
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		WriteFile: os.WriteFile,
	}
}

// Env is the global [*Environ].
var Env = NewEnvironOS()
