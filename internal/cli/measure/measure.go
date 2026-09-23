// SPDX-License-Identifier: GPL-3.0-or-later

// Package measure implements the `sonda measure` subcommand.
package measure

import (
	"context"

	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vclip"
	"github.com/bassosimone/vflag"
)

// ShortDescr is the command short description.
const ShortDescr = "Run a single low-level network measurement."

// Main is the main function of the `sonda measure` subcommand.
func Main(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Create the `sonda measure dns over` dispatcher.
	overCmd := vclip.NewDispatcherCommand("over", vflag.ExitOnError)
	overCmd.Exit = env.Exit
	overCmd.Stderr = env.Stderr
	overCmd.Stdout = env.UsageStdout

	overCmd.AddDescription("Select the transport protocol.")
	overCmd.AddCommand("https", vclip.CommandFunc(dnsOverHTTPSMain), "DNS over HTTPS (DoH).")
	overCmd.AddCommand("udp", vclip.CommandFunc(dnsOverUDPMain), "DNS over UDP.")

	// Create the `sonda measure dns` dispatcher.
	dnsCmd := vclip.NewDispatcherCommand("dns", vflag.ExitOnError)
	dnsCmd.Exit = env.Exit
	dnsCmd.Stderr = env.Stderr
	dnsCmd.Stdout = env.UsageStdout

	dnsCmd.AddDescription("Run DNS measurements.")
	dnsCmd.AddCommand("over", overCmd, "Select the DNS transport protocol.")

	// Create the `sonda measure serve` dispatcher.
	serveCmd := vclip.NewDispatcherCommand("serve", vflag.ExitOnError)
	serveCmd.Exit = env.Exit
	serveCmd.Stderr = env.Stderr
	serveCmd.Stdout = env.Stdout

	serveCmd.AddDescription("Serve multiple measurement requests.")
	serveCmd.AddCommand(
		"stdio", vclip.CommandFunc(serveStdioMain),
		"Read requests from stdin and write events to stdout.")

	// Create the `sonda measure` dispatcher.
	disp := vclip.NewDispatcherCommand("measure", vflag.ExitOnError)
	disp.Exit = env.Exit
	disp.Stderr = env.Stderr
	disp.Stdout = env.UsageStdout

	disp.AddDescription(ShortDescr)
	disp.AddCommand("dns", dnsCmd, "Run DNS measurements.")
	disp.AddCommand("http", vclip.CommandFunc(httpMain), "Run HTTP measurement.")
	disp.AddCommand("https", vclip.CommandFunc(httpsMain), "Run HTTPS measurement.")
	disp.AddCommand("serve", serveCmd, "Serve multiple measurement requests.")
	disp.AddCommand("stun", vclip.CommandFunc(stunMain), "STUN binding request.")

	disp.Main(ctx, args)
	return nil
}
