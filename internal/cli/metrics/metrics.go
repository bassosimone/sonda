// SPDX-License-Identifier: GPL-3.0-or-later

// Package metrics implements the `sonda metrics` subcommand.
package metrics

import (
	"context"

	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vclip"
	"github.com/bassosimone/vflag"
)

// ShortDescr is the command short description.
const ShortDescr = "Aggregate and query measurement metrics."

// Main is the main function of the `sonda metrics` subcommand.
func Main(ctx context.Context, args []string) error {
	env := testable.ContextEnviron(ctx)

	// Create the `sonda metrics` dispatcher.
	disp := vclip.NewDispatcherCommand("metrics", vflag.ExitOnError)
	disp.Exit = env.Exit
	disp.Stderr = env.Stderr
	disp.Stdout = env.UsageStdout
	disp.AddDescription(ShortDescr)
	disp.AddCommand("extract", vclip.CommandFunc(extractMain), "Extract Parquet from span directories.")
	disp.AddCommand("load", vclip.CommandFunc(loadMain), "Aggregate span metrics into daily Parquet files.")

	disp.Main(ctx, args)
	return nil
}
