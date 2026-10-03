// SPDX-License-Identifier: GPL-3.0-or-later

// Package plugincommand contains code to implement a plugin command.
package plugincommand

import (
	"context"
	"os"

	"github.com/bassosimone/deferexit"
	"github.com/bassosimone/sonda/internal/reexec"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vclip"
)

// Flags for [Main].
const (
	FlagAllowReExec = 1 << iota
)

// Main is the plugin command main. The `main` argument is the actual main
// implementation. This function will create the required environment around
// it and then defer the actual execution to it.
func Main(flags int, main func(ctx context.Context, args []string) error) {
	// Transform panics into [os.Exit] calls.
	defer deferexit.Recover(os.Exit)
	env := testable.Env

	// Get the context early because it may be modified again later.
	ctx := context.Background()

	// Arrange for code re-execution to work as intended.
	if flags&FlagAllowReExec != 0 {
		ctx = reexec.WithReExecFeature(ctx)
	}

	// Wrap the real main w/ `vclip.RootCommand`.
	root := vclip.NewRootCommand(vclip.CommandFunc(main))
	root.LogFatalOnError0 = env.LogFatalOnError0

	// Execute through the wrapper command.
	root.Main(ctx, env.Args[1:])
}
