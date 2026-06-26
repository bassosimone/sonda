// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"os"

	"github.com/bassosimone/deferexit"
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
	disp.Stdout = env.Stdout

	// Add subcommands.
	disp.AddCommand("measure", vclip.CommandFunc(measure.Main), "Run a single low-level network measurement.")
	disp.AddCommand("metrics", vclip.CommandFunc(metrics.Main), "Aggregate and query measurement metrics.")
	disp.AddCommand("scan", vclip.CommandFunc(scan.Main), "Scan specific network endpoints storing results in the spool.")
	disp.AddCommand("spool", vclip.CommandFunc(spool.Main), "Manage the measurement spool directory.")

	// Execute the root dispatcher command.
	disp.Main(context.Background(), env.Args[1:])
}
