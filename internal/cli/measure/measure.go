// SPDX-License-Identifier: GPL-3.0-or-later

// Package measure implements the `sonda measure` subcommand.
package measure

import (
	"context"

	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vclip"
	"github.com/bassosimone/vflag"
)

// Main is the main function of the `sonda measure` subcommand.
func Main(ctx context.Context, args []string) error {
	env := testable.Env

	// Create the `sonda measure dns over` dispatcher.
	overCmd := vclip.NewDispatcherCommand("over", vflag.ExitOnError)
	overCmd.Exit = env.Exit
	overCmd.Stderr = env.Stderr
	overCmd.Stdout = env.Stdout
	overCmd.AddDescription("Select the transport protocol.")
	overCmd.AddCommand("https", vclip.CommandFunc(dnsOverHTTPSMain), "DNS over HTTPS (DoH).")
	overCmd.AddCommand("udp", vclip.CommandFunc(dnsOverUDPMain), "DNS over UDP.")

	// Create the `sonda measure dns` dispatcher.
	dnsCmd := vclip.NewDispatcherCommand("dns", vflag.ExitOnError)
	dnsCmd.Exit = env.Exit
	dnsCmd.Stderr = env.Stderr
	dnsCmd.Stdout = env.Stdout
	dnsCmd.AddDescription("Run DNS measurements.")
	dnsCmd.AddCommand("over", overCmd, "Select the DNS transport protocol.")

	// Create the `sonda measure` dispatcher.
	disp := vclip.NewDispatcherCommand("measure", vflag.ExitOnError)
	disp.Exit = env.Exit
	disp.Stderr = env.Stderr
	disp.Stdout = env.Stdout
	disp.AddDescription("Run a single network measurement.")
	disp.AddCommand("dns", dnsCmd, "Run DNS measurements.")

	disp.Main(ctx, args)
	return nil
}
