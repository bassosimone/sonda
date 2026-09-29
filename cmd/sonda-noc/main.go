// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"os"

	"github.com/bassosimone/deferexit"
	"github.com/bassosimone/sonda/internal/plugins/noc"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vclip"
)

func main() {
	// Transform panics into [os.Exit] calls.
	defer deferexit.Recover(os.Exit)
	env := testable.Env

	// Wrap the actual command using `vclip.RootCommand`.
	root := vclip.NewRootCommand(vclip.CommandFunc(noc.Main))
	root.LogFatalOnError0 = env.LogFatalOnError0

	// Execute the dispatcher command wrapper.
	root.Main(context.Background(), env.Args[1:])
}
