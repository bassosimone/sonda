// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/bassosimone/deferexit"
	"github.com/bassosimone/sonda/internal/buildcfg"
	"github.com/bassosimone/sonda/internal/plugins"
	"github.com/bassosimone/sonda/internal/reexec"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vclip"
	"github.com/bassosimone/vflag"
)

func main() {
	// Transform panics into [os.Exit] calls.
	defer deferexit.Recover(os.Exit)
	env := testable.Env

	// Set `SONDA_COMMAND` so that plugins can invoke `sonda` back.
	ctx := context.Background()
	exePath, err := env.Executable()
	if err != nil {
		fmt.Fprintf(env.Stderr, "sonda: %s\n", err.Error())
		env.Exit(1)
	}
	env = testable.WithEnvOverrides(env, "SONDA_COMMAND="+exePath)
	ctx = testable.WithEnviron(ctx, env)

	// Arrange for code re-execution to work as intended.
	env.ReExec = reexec.Subcommand
	env.AsExitCode = reexec.AsExitCode

	// Create and init the root dispatcher command.
	disp := vclip.NewDispatcherCommand("sonda", vflag.ExitOnError)
	disp.Exit = env.Exit
	disp.Stderr = env.Stderr
	disp.Stdout = env.UsageStdout
	disp.AddDescription(
		"Experimental network probe. It measures STUN, DNS over UDP, DNS " +
			"over HTTPS, and HTTPS, and stores the raw results in a local " +
			"spool. It then extracts quality of experience (QoE) metrics " +
			"from the spool into daily Parquet files for later analysis.")

	// Wire version reporting.
	disp.AddVersionHandlers(buildcfg.Version)

	// Plugins subcommands (after builtins so they can't register existing names).
	if err := plugins.Load(env, disp); err != nil {
		fmt.Fprintf(env.Stderr, "sonda: cannot load plugins: %s\n", err.Error())
		env.Exit(1)
	}

	// Wrap the root dispatcher using `vclip.RootCommand`.
	root := vclip.NewRootCommand(disp)
	root.LogFatalOnError0 = env.LogFatalOnError0

	// Execute the dispatcher command wrapper.
	root.Main(ctx, env.Args[1:])
}
