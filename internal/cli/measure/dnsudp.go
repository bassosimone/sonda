// SPDX-License-Identifier: GPL-3.0-or-later

package measure

import (
	"context"
	"log/slog"
	"net/netip"
	"time"

	"github.com/bassosimone/dnscodec"
	"github.com/bassosimone/errclass"
	"github.com/bassosimone/nop"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
	"github.com/miekg/dns"
)

// dnsOverUDPMain is the main function of the `sonda measure dns over udp` subcommand.
func dnsOverUDPMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.Env

	// Set command defaults.
	var (
		domain    = "www.example.com"
		queryType = "A"
		target    = "8.8.8.8:53"
		spanID    = nop.NewSpanID()
		timeout   = 30 * time.Second
	)

	// Parse command line flags
	fset := vflag.NewFlagSet("sonda measure dns over udp", vflag.ExitOnError)
	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.Stdout
	fset.StringVar(&domain, 0, "domain", "Use `NAME` instead of `@DEFAULT_VALUE@`.")
	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.StringVar(&queryType, 0, "query-type", "Use `TYPE` instead of `@DEFAULT_VALUE@`.")
	fset.StringVar(&target, 0, "target", "Use `ADDR:PORT` instead of `@DEFAULT_VALUE@`.")
	fset.DurationVar(&timeout, 0, "timeout", "Use `DURATION` instead of `@DEFAULT_VALUE@`.")
	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Emit structured logs to the stdout tied together by a span ID.
	logger := slog.New(slog.NewJSONHandler(env.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	logger = logger.With("spanID", spanID)

	// Log the measurement start / done events
	logger.Info("measurementStart")
	defer logger.Info("measurementDone")

	// Log the command line arguments for reproducibility.
	fullArgs := append([]string{"sonda", "measure", "dns", "over", "udp"}, args...)
	logger.Info("commandLineArgs", slog.Any("args", fullArgs))

	// Parse the query type string.
	dnsType, ok := dns.StringToType[queryType]
	if !ok {
		logger.Error("parseQueryTypeFailed", slog.String("queryType", queryType))
		env.Exit(2)
	}

	// Parse target as an endpoint.
	epnt, err := netip.ParseAddrPort(target)
	if err != nil {
		logger.Error("parseTargetFailed", slog.Any("err", err))
		env.Exit(2)
	}

	// Configure the pipeline timeout.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create the shared pipeline configuration.
	cfg := nop.NewConfig()
	cfg.ErrClassifier = nop.ErrClassifierFunc(errclass.New)

	// Create the dialing pipeline.
	epntOp := nop.NewEndpointFunc(epnt)
	connectOp := nop.NewConnectFunc(cfg, "udp", logger)
	observeOp := nop.NewObserveConnFunc(cfg, logger)
	autoCancelOp := nop.NewCancelWatchFunc()
	wrapOp := nop.NewDNSOverUDPConnFunc(cfg, logger)
	dialPipe := nop.Compose5(epntOp, connectOp, observeOp, autoCancelOp, wrapOp)

	// Dial the DNS connection.
	dnsConn, err := dialPipe.Call(ctx, nop.Unit{})
	if err != nil {
		logger.Error("dialFailed", slog.Any("err", err))
		env.Exit(1)
	}
	defer dnsConn.Close()

	// Perform the DNS exchange.
	dnsQuery := dnscodec.NewQuery(domain, dnsType)
	dnsResp, err := dnsConn.Exchange(ctx, dnsQuery)
	if err != nil {
		logger.Error("exchangeFailed", slog.Any("err", err))
		env.Exit(1)
	}

	// Print A, AAAA, and CNAME records.
	// TODO(bassosimone): we may want to support logging other response types.
	if cnames, err := dnsResp.RecordsCNAME(); err == nil {
		logger.Info("responseCNAME", slog.Any("value", cnames))
	}
	if addrs, err := dnsResp.RecordsA(); err == nil {
		logger.Info("responseA", slog.Any("value", addrs))
	}
	if addrs, err := dnsResp.RecordsAAAA(); err == nil {
		logger.Info("responseAAAA", slog.Any("value", addrs))
	}
	return nil
}
