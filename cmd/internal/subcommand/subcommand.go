// SPDX-License-Identifier: GPL-3.0-or-later

// Package subcommand contains code to implement a subcommand.
package subcommand

import (
	"context"
	"errors"
	"os"

	"github.com/bassosimone/deferexit"
	"github.com/bassosimone/sonda/internal/reexec"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vclip"
)

// Main is the main subcommand function.
func Main(main func(ctx context.Context, args []string) error) {
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

	// Wrap the measure command w/ `vclip.RootCommand`.
	root := vclip.NewRootCommand(vclip.CommandFunc(main))
	root.LogFatalOnError0 = env.LogFatalOnError0

	// Execute the measure command wrapper.
	root.Main(context.Background(), env.Args[1:])
}
