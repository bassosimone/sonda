// SPDX-License-Identifier: GPL-3.0-or-later

// Package testable contains code to make sonda testable.
package testable

import (
	"io"
	"os"

	"github.com/bassosimone/deferexit"
)

// Environ abstracts away side effects (I/O, exit) so that commands
// can be tested without real I/O or process termination.
type Environ struct {
	Args   []string
	Exit   func(code int)
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// NewEnvironOS returns an [*Environ] wired to real OS operations.
func NewEnvironOS() *Environ {
	return &Environ{
		Args:   os.Args,
		Exit:   deferexit.Panic,
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
}

// Env is the global [*Environ].
var Env = NewEnvironOS()
