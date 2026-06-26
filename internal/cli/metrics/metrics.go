// SPDX-License-Identifier: GPL-3.0-or-later

// Package metrics implements the `sonda metrics` subcommand.
package metrics

import (
	"context"

	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vclip"
	"github.com/bassosimone/vflag"
)

// Main is the main function of the `sonda metrics` subcommand.
func Main(ctx context.Context, args []string) error {
	env := testable.Env

	// Create the `sonda metrics` dispatcher.
	disp := vclip.NewDispatcherCommand("metrics", vflag.ExitOnError)
	disp.Exit = env.Exit
	disp.Stderr = env.Stderr
	disp.Stdout = env.Stdout
	disp.AddDescription("Aggregate and query measurement metrics.")
	disp.AddCommand("load", vclip.CommandFunc(loadMain), "Aggregate span metrics into daily Parquet files.")

	disp.Main(ctx, args)
	return nil
}
