// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"

	"github.com/bassosimone/sonda/cmd/internal/plugincommand"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vclip"
	"github.com/bassosimone/vflag"
)

func main() {
	plugincommand.Main(plugincommand.FlagAllowReExec, realMain)
}

const shortDescr = "Manage the measurement spool directory."

func realMain(ctx context.Context, args []string) error {
	env := testable.ContextEnviron(ctx)

	// Create the `sonda-spool` dispatcher.
	disp := vclip.NewDispatcherCommand("sonda-spool", vflag.ExitOnError)
	disp.Exit = env.Exit
	disp.Stderr = env.Stderr
	disp.Stdout = env.UsageStdout
	disp.AddDescription(shortDescr)
	disp.AddCommand("gc", vclip.CommandFunc(gcMain), "Remove old span directories.")
	disp.AddCommand("run", vclip.CommandFunc(runMain), "Execute a sonda subcommand and collect its output.")

	disp.Main(ctx, args)
	return nil
}
