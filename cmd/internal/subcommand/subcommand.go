// SPDX-License-Identifier: GPL-3.0-or-later

// Package subcommand contains code shared by subcommands.
package subcommand

import (
	"context"
	"errors"
	"os"

	"github.com/bassosimone/deferexit"
	"github.com/bassosimone/sonda/internal/reexec"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vclip"
	"github.com/bassosimone/vflag"
)

// Main implements the main function of a generic subcommand.
//
// Arguments:
//
// 1. `name` is the full name of the subcommand (e.g. `sonda-foo`).
//
// 2. `init` initializes the dispatcher.
//
// 3. `docs` provides the command documentation.
func Main(name string, init func(disp *vclip.DispatcherCommand), docs ...string) {
	// Transform panics into [os.Exit] calls.
	defer deferexit.Recover(os.Exit)
	env := testable.Env

	// Arrange for code re-execution to work as intended.
	env.ReExec = reexec.Subcommand
	env.AsExitCode = reexec.AsExitCode
	getenv := env.Getenv
	env.Executable = func() (string, error) {
		value := getenv("SONDA_COMMAND")
		if value == "" {
			return "", errors.New("SONDA_COMMAND is not defined")
		}
		return value, nil
	}

	// Create and init the root dispatcher command.
	disp := vclip.NewDispatcherCommand(name, vflag.ExitOnError)
	disp.Exit = env.Exit
	disp.Stderr = env.Stderr
	disp.Stdout = env.UsageStdout

	// Add the command docs.
	disp.AddDescription(docs...)

	// Built-in subcommands.
	init(disp)

	// Wrap the root dispatcher using `vclip.RootCommand`.
	root := vclip.NewRootCommand(disp)
	root.LogFatalOnError0 = env.LogFatalOnError0

	// Execute the dispatcher command wrapper.
	root.Main(context.Background(), env.Args[1:])
}
