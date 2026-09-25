// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"os"

	"github.com/bassosimone/deferexit"
	"github.com/bassosimone/sonda/internal/buildcfg"
	"github.com/bassosimone/sonda/internal/cli/metrics"
	"github.com/bassosimone/sonda/internal/cli/scan"
	"github.com/bassosimone/sonda/internal/reexec"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vclip"
	"github.com/bassosimone/vflag"
)

func main() {
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
	disp := vclip.NewDispatcherCommand("sonda-qoe", vflag.ExitOnError)
	disp.Exit = env.Exit
	disp.Stderr = env.Stderr
	disp.Stdout = env.UsageStdout

	// Wire version reporting.
	disp.AddVersionHandlers(buildcfg.Version)

	// Built-in subcommands.
	disp.AddCommand("metrics", vclip.CommandFunc(metrics.Main), metrics.ShortDescr)
	disp.AddCommand("scan", vclip.CommandFunc(scan.Main), scan.ShortDescr)

	// Wrap the root dispatcher using `vclip.RootCommand`.
	root := vclip.NewRootCommand(disp)
	root.LogFatalOnError0 = env.LogFatalOnError0

	// Execute the dispatcher command wrapper.
	root.Main(context.Background(), env.Args[1:])
}
