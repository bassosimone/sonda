// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"os"

	"github.com/bassosimone/deferexit"
	"github.com/bassosimone/sonda/internal/cli/measure"
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

	// Add the measure subcommand.
	disp.AddCommand("measure", vclip.CommandFunc(measure.Main), "Run a single network measurement.")

	// Execute the root dispatcher command.
	disp.Main(context.Background(), env.Args[1:])
}
