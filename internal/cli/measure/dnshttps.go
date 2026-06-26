// SPDX-License-Identifier: GPL-3.0-or-later

package measure

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/netip"
	"net/url"
	"time"

	"github.com/bassosimone/dnscodec"
	"github.com/bassosimone/errclass"
	"github.com/bassosimone/nop"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
	"github.com/miekg/dns"
)

// dnsOverHTTPSMain is the main function of the `sonda measure dns over https` subcommand.
func dnsOverHTTPSMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.Env

	// Set command defaults.
	var (
		domain    = "www.example.com"
		httpHost  = "dns.google"
		queryType = "A"
		sni       = "dns.google"
		target    = "8.8.8.8:443"
		urlPath   = "/dns-query"
		spanID    = nop.NewSpanID()
		timeout   = 30 * time.Second
	)

	// Honor SONDA_SPAN_ID environment variable.
	if v := env.Getenv("SONDA_SPAN_ID"); v != "" {
		spanID = v
	}

	// Parse command line flags
	fset := vflag.NewFlagSet("sonda measure dns over https", vflag.ExitOnError)
	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.Stdout
	fset.StringVar(&domain, 0, "domain", "Use `NAME` instead of `@DEFAULT_VALUE@`.")
	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.StringVar(&httpHost, 0, "http-host", "Use `NAME` instead of `@DEFAULT_VALUE@`.")
	fset.StringVar(&queryType, 0, "query-type", "Use `TYPE` instead of `@DEFAULT_VALUE@`.")
	fset.StringVar(&sni, 0, "sni", "Use `NAME` instead of `@DEFAULT_VALUE@`.")
	fset.StringVar(&spanID, 0, "span-id", "Use `ID` instead of a random one. Honors `SONDA_SPAN_ID`.")
	fset.StringVar(&target, 0, "target", "Use `ADDR:PORT` instead of `@DEFAULT_VALUE@`.")
	fset.DurationVar(&timeout, 0, "timeout", "Use `DURATION` instead of `@DEFAULT_VALUE@`.")
	fset.StringVar(&urlPath, 0, "url-path", "Use `PATH` instead of `@DEFAULT_VALUE@`.")
	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Emit structured logs to the stdout tied together by a span ID.
	logger := slog.New(slog.NewJSONHandler(env.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	logger = logger.With("spanID", spanID)

	// Log the command start / done span events.
	t0 := time.Now()
	logger.Info("sondaCommandStart", slog.Time("t", t0))
	defer func() {
		logger.Info("sondaCommandDone", slog.Time("t0", t0), slog.Time("t", time.Now()))
	}()

	// Log the command line arguments for reproducibility.
	fullArgs := append([]string{"sonda", "measure", "dns", "over", "https"}, args...)
	logger.Info("sondaCommandLineArgs", slog.Any("cliArgs", fullArgs))

	// Parse the query type string.
	dnsType, ok := dns.StringToType[queryType]
	if !ok {
		logger.Error("sondaFailure", slog.String("operation", "parseQueryType"), slog.String("err", "unknown query type"))
		env.Exit(2)
	}

	// Parse target as an endpoint.
	epnt, err := netip.ParseAddrPort(target)
	if err != nil {
		logger.Error("sondaFailure", slog.String("operation", "parseTarget"), slog.Any("err", err))
		env.Exit(2)
	}

	// Configure the pipeline timeout.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create the shared pipeline configuration.
	cfg := nop.NewConfig()
	cfg.Dialer = env.Dialer
	cfg.ErrClassifier = nop.ErrClassifierFunc(errclass.New)

	// Create the dialing pipeline.
	epntOp := nop.NewEndpointFunc(epnt)
	connectOp := nop.NewConnectFunc(cfg, "tcp", logger)
	observeOp := nop.NewObserveConnFunc(cfg, logger)
	autoCancelOp := nop.NewCancelWatchFunc()
	tlsConfig := &tls.Config{ServerName: sni, NextProtos: []string{"h2", "http/1.1"}}
	tlsHandshakeOp := nop.NewTLSHandshakeFunc(cfg, tlsConfig, logger)
	httpConnOp := nop.NewHTTPConnFuncTLS(cfg, logger)
	dohURL := (&url.URL{Scheme: "https", Host: httpHost, Path: urlPath}).String()
	wrapOp := nop.NewDNSOverHTTPSConnFunc(cfg, dohURL, logger)
	dialPipe := nop.Compose7(epntOp, connectOp, observeOp, autoCancelOp, tlsHandshakeOp, httpConnOp, wrapOp)

	// Dial the DNS connection.
	dnsConn, err := dialPipe.Call(ctx, nop.Unit{})
	if err != nil {
		logger.Error("sondaFailure", slog.String("operation", "dial"), slog.Any("err", err))
		env.Exit(1)
	}
	defer dnsConn.Close()

	// Perform the DNS exchange.
	dnsQuery := dnscodec.NewQuery(domain, dnsType)
	dnsResp, err := dnsConn.Exchange(ctx, dnsQuery)
	if err != nil {
		logger.Error("sondaFailure", slog.String("operation", "exchange"), slog.Any("err", err))
		env.Exit(1)
	}

	// Print A, AAAA, and CNAME records.
	// TODO(bassosimone): we may want to support logging other response types.
	if cnames, err := dnsResp.RecordsCNAME(); err == nil {
		logger.Info("sondaDnsRecordsCNAME", slog.Any("dnsRecordsList", cnames))
	}
	if addrs, err := dnsResp.RecordsA(); err == nil {
		logger.Info("sondaDnsRecordsA", slog.Any("dnsRecordsList", addrs))
	}
	if addrs, err := dnsResp.RecordsAAAA(); err == nil {
		logger.Info("sondaDnsRecordsAAAA", slog.Any("dnsRecordsList", addrs))
	}
	return nil
}
