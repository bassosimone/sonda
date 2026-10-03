// SPDX-License-Identifier: GPL-3.0-or-later

// Package spool implements the `sonda spool` subcommand.
package spool

import (
	"context"

	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vclip"
	"github.com/bassosimone/vflag"
)

// ShortDescr is the short description of the command.
const ShortDescr = "Manage the measurement spool directory."

// Main is the main function of the `sonda spool` subcommand.
func Main(ctx context.Context, args []string) error {
	env := testable.ContextEnviron(ctx)

	// Create the `sonda spool` dispatcher.
	disp := vclip.NewDispatcherCommand("spool", vflag.ExitOnError)
	disp.Exit = env.Exit
	disp.Stderr = env.Stderr
	disp.Stdout = env.UsageStdout
	disp.AddDescription(ShortDescr)
	disp.AddCommand("gc", vclip.CommandFunc(gcMain), "Remove old span directories.")
	disp.AddCommand("run", vclip.CommandFunc(runMain), "Execute a sonda subcommand and collect its output.")

	disp.Main(ctx, args)
	return nil
}
