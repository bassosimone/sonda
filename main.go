// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"os"

	"github.com/bassosimone/deferexit"
	"github.com/bassosimone/sonda/internal/buildcfg"
	"github.com/bassosimone/sonda/internal/cli/measure"
	"github.com/bassosimone/sonda/internal/cli/metrics"
	"github.com/bassosimone/sonda/internal/cli/scan"
	"github.com/bassosimone/sonda/internal/cli/spool"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vclip"
	"github.com/bassosimone/vflag"
)

func main() {
	// Transform panics into [os.Exit] calls.
	defer deferexit.Recover(os.Exit)
	env := testable.Env

	// Create and init the root dispatcher command.
	disp := vclip.NewDispatcherCommand("sonda", vflag.ExitOnError)
	disp.Exit = env.Exit
	disp.Stderr = env.Stderr
	disp.Stdout = env.UsageStdout

	// Wire version reporting.
	disp.AddVersionHandlers(buildcfg.Version)

	// Add subcommands.
	disp.AddCommand("measure", vclip.CommandFunc(measure.Main), measure.ShortDescr)
	disp.AddCommand("metrics", vclip.CommandFunc(metrics.Main), metrics.ShortDescr)
	disp.AddCommand("scan", vclip.CommandFunc(scan.Main), scan.ShortDescr)
	disp.AddCommand("spool", vclip.CommandFunc(spool.Main), spool.ShortDescr)

	// Wrap the root dispatcher using `vclip.RootCommand`.
	root := vclip.NewRootCommand(disp)
	root.LogFatalOnError0 = env.LogFatalOnError0

	// Execute the dispatcher command wrapper.
	root.Main(context.Background(), env.Args[1:])
}
