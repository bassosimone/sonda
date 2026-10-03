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
	plugincommand.Main(0, realMain)
}

const shortDescr = "Extract and load QoE metrics from ptnop logs."

func realMain(ctx context.Context, args []string) error {
	env := testable.ContextEnviron(ctx)

	// Create the `sonda-etl-ptnop-qoe` dispatcher.
	disp := vclip.NewDispatcherCommand("sonda-etl-ptnop-qoe", vflag.ExitOnError)
	disp.Exit = env.Exit
	disp.Stderr = env.Stderr
	disp.Stdout = env.UsageStdout
	disp.AddDescription(shortDescr)
	disp.AddCommand("extract", vclip.CommandFunc(extractMain), "Extract Parquet from span directories.")
	disp.AddCommand("load", vclip.CommandFunc(loadMain), "Aggregate span metrics into daily Parquet files.")

	disp.Main(ctx, args)
	return nil
}
