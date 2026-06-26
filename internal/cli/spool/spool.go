// SPDX-License-Identifier: GPL-3.0-or-later

// Package spool implements the `sonda spool` subcommand.
package spool

import (
	"context"

	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vclip"
	"github.com/bassosimone/vflag"
)

// Main is the main function of the `sonda spool` subcommand.
func Main(ctx context.Context, args []string) error {
	env := testable.Env

	// Create the `sonda spool` dispatcher.
	disp := vclip.NewDispatcherCommand("spool", vflag.ExitOnError)
	disp.Exit = env.Exit
	disp.Stderr = env.Stderr
	disp.Stdout = env.Stdout
	disp.AddDescription("Manage the measurement spool directory.")
	disp.AddCommand("extract", vclip.CommandFunc(extractMain), "Extract Parquet from span directories.")
	disp.AddCommand("gc", vclip.CommandFunc(gcMain), "Remove old span directories.")
	disp.AddCommand("run", vclip.CommandFunc(runMain), "Execute a command and collect its output.")

	disp.Main(ctx, args)
	return nil
}
