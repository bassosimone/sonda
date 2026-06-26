// SPDX-License-Identifier: GPL-3.0-or-later

// Package scan implements the `sonda scan` subcommand.
package scan

import (
	"context"
	"log/slog"
	"net"
	"net/http"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/netstack"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
)

// Main is the main function of the `sonda scan` subcommand.
func Main(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.Env

	// Set command defaults.
	var (
		fail     = false
		spoolDir = "."
	)

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda scan", vflag.ExitOnError)
	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.Stdout
	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.BoolVar(&fail, 'f', "fail", "Exit with error on first failure.")
	fset.StringVar(&spoolDir, 0, "spool-dir", "Use `DIR` instead of `@DEFAULT_VALUE@`.")
	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Emit structured logs to stderr.
	logger := slog.New(slog.NewJSONHandler(env.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Honor the `-f/--fail` command line flag.
	maybeExit := env.Exit
	if !fail {
		maybeExit = func(_ int) {}
	}

	// Create the measurer for running operations through the spool.
	measurer := netstack.NewSondaMeasurer(env, spoolDir)

	// Resolve stun.l.google.com to obtain STUN server addresses.
	resolver := netstack.NewResolver(netstack.NewDNSOverUDPTransport(measurer))
	stunAddrs, err := resolver.LookupHost(ctx, "stun.l.google.com")
	if err != nil {
		logger.Warn("resolveSTUNServerFailed", slog.Any("err", err))
		maybeExit(1)
	}

	// Perform STUN lookups to discover the reflexive address.
	if len(stunAddrs) > 0 {
		if _, err := stunLookup(ctx, measurer, stunAddrs, "19302"); err != nil {
			logger.Warn("stunLookupFailed", slog.Any("err", err))
			maybeExit(1)
		}
	}

	// Resolve dns.google to obtain DNS server addresses.
	dnsAddrs, err := resolver.LookupHost(ctx, "dns.google")
	if err != nil {
		logger.Warn("resolveDNSServerFailed", slog.Any("err", err))
		maybeExit(1)
	}

	// Perform DNS-over-UDP lookups for www.example.com against each address.
	for _, addr := range dnsAddrs {
		udp := netstack.NewDNSOverUDPTransport(measurer)
		udp.ServerAddr = net.JoinHostPort(addr, "53")
		r := netstack.NewResolver(udp)
		if _, err := r.LookupHost(ctx, "www.example.com"); err != nil {
			logger.Warn("dnsOverUDPFailed", slog.String("addr", addr), slog.Any("err", err))
			maybeExit(1)
		}
	}

	// Perform DNS-over-HTTPS lookups for www.example.com against each address.
	for _, addr := range dnsAddrs {
		doh := netstack.NewDNSOverHTTPSTransport(measurer)
		doh.ServerAddr = net.JoinHostPort(addr, "443")
		r := netstack.NewResolver(doh)
		if _, err := r.LookupHost(ctx, "www.example.com"); err != nil {
			logger.Warn("dnsOverHTTPSFailed", slog.String("addr", addr), slog.Any("err", err))
			maybeExit(1)
		}
	}

	// TODO(bassosimone): the HTTPTransport tries addresses sequentially and
	// stops at the first success. For measurement purposes, we should instead
	// resolve explicitly and run SondaMeasureHTTPS against each address.

	// Perform an HTTPS GET of https://www.example.com/.
	txp := netstack.NewHTTPTransport(measurer, resolver)
	httpsReq, err := http.NewRequestWithContext(ctx, "GET", "https://www.example.com/", http.NoBody)
	if err != nil {
		logger.Warn("newHTTPSRequestFailed", slog.Any("err", err))
		maybeExit(1)
	} else {
		resp, err := txp.RoundTrip(httpsReq)
		if err != nil {
			logger.Warn("httpsGetFailed", slog.Any("err", err))
			maybeExit(1)
		} else {
			resp.Body.Close()
		}
	}

	return nil
}
