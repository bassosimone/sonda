// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/bassosimone/errclass"
	"github.com/bassosimone/nop"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
)

// scanFiles contains the files that `scanMain` should be aware of.
type scanFiles struct {
	// body is where to write the response body.
	body io.Writer

	// stdout is where to write the structured logs.
	stdout io.Writer

	// stderr is where to write errors.
	stderr io.Writer
}

// scanMain executes a single network scan according to the given flags.
func scanMain(ctx context.Context, files *scanFiles, spanID string, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Set command defaults.
	var (
		headers  []string
		httpHost = "1.1.1.1"
		method   = "GET"
		sni      = "1.1.1.1"
		tags     []string
		target   = "1.1.1.1:443"
		timeout  = 30 * time.Second
		urlPath  = "/"
	)

	// Parse command line flags.
	fset := vflag.NewFlagSet("scan", vflag.ContinueOnError)

	fset.Exit = env.Exit
	fset.Stderr = io.Discard
	fset.Stdout = io.Discard

	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.StringSliceVar(&headers, 'H', "header", "Add `KEY: VALUE` request header. Repeatable.")
	fset.StringVar(&httpHost, 0, "http-host", "Use `NAME` instead of `@DEFAULT_VALUE@`.")
	fset.StringVar(&method, 0, "method", "Use `METHOD` instead of `@DEFAULT_VALUE@`.")
	fset.StringVar(&sni, 0, "sni", "Use `NAME` instead of `@DEFAULT_VALUE@`.")
	fset.StringVar(&spanID, 0, "span-id", "Use `ID` instead of a random one. Honors `SONDA_SPAN_ID`.")
	fset.StringSliceVar(&tags, 0, "tag", "Add contextual `KEY=VALUE` tag. Repeatable.")
	fset.StringVar(&target, 0, "target", "Use `ADDR:PORT` instead of `@DEFAULT_VALUE@`.")
	fset.DurationVar(&timeout, 0, "timeout", "Use `DURATION` instead of `@DEFAULT_VALUE@`.")
	fset.StringVar(&urlPath, 0, "url-path", "Use `PATH` instead of `@DEFAULT_VALUE@`.")

	if err := fset.Parse(args); err != nil {
		// XXX: how to log? maybe this should be a method?
		return err
	}

	// Emit structured logs to the stdout tied together by a span ID.
	logger := slog.New(slog.NewJSONHandler(files.stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	logger = logger.With("spanID", spanID)
	for _, tag := range tags {
		if key, value, ok := strings.Cut(tag, "="); ok {
			logger = logger.With(key, value)
		}
	}

	// Parse target as an endpoint.
	epnt, err := netip.ParseAddrPort(target)
	if err != nil {
		logger.Error(
			"sondaFailure",
			slog.String("operation", "parseTarget"),
			slog.Any("err", err),
			slog.Int("exitCode", 2),
		)
		return err
	}

	// Configure the pipeline timeout.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create the shared pipeline configuration.
	cfg := nop.NewConfig()
	cfg.Dialer = env.Dialer
	cfg.ErrClassifier = nop.ErrClassifierFunc(errclass.New)

	// Create the dialing pipeline (TCP connect, TLS handshake, HTTPS).
	epntOp := nop.NewEndpointFunc(epnt)
	connectOp := nop.NewConnectFunc(cfg, "tcp", logger)
	observeOp := nop.NewObserveConnFunc(cfg, logger)
	autoCancelOp := nop.NewCancelWatchFunc()
	tlsConfig := &tls.Config{ServerName: sni, NextProtos: []string{"h2", "http/1.1"}}
	tlsHandshakeOp := nop.NewTLSHandshakeFunc(cfg, tlsConfig, logger)
	httpConnOp := nop.NewHTTPConnFuncTLS(cfg, logger)
	dialPipe := nop.Compose6(epntOp, connectOp, observeOp, autoCancelOp, tlsHandshakeOp, httpConnOp)

	// Dial the HTTPS connection.
	httpConn, err := dialPipe.Call(ctx, nop.Unit{})
	if err != nil {
		logger.Error(
			"sondaFailure",
			slog.String("operation", "dial"),
			slog.Any("err", err),
			slog.Int("exitCode", 1),
		)
		return err
	}
	defer httpConn.Close()

	// Build the HTTP request.
	httpURL := (&url.URL{Scheme: "https", Host: httpHost, Path: urlPath}).String()
	httpReq, err := http.NewRequestWithContext(ctx, method, httpURL, http.NoBody)
	if err != nil {
		logger.Error(
			"sondaFailure",
			slog.String("operation", "newRequest"),
			slog.Any("err", err),
			slog.Int("exitCode", 1),
		)
		return err
	}
	for _, h := range headers {
		key, value, ok := strings.Cut(h, ":")
		if !ok {
			logger.Error(
				"sondaFailure",
				slog.String("operation", "parseHeader"),
				slog.String("header", h),
				slog.String("err", "missing colon"),
				slog.Int("exitCode", 2),
			)
			return err
		}
		httpReq.Header.Add(strings.TrimSpace(key), strings.TrimSpace(value))
	}

	// Perform the HTTP round trip.
	resp, err := httpConn.RoundTrip(httpReq)
	if err != nil {
		logger.Error(
			"sondaFailure",
			slog.String("operation", "roundTrip"),
			slog.Any("err", err),
			slog.Int("exitCode", 1),
		)
		return err
	}
	defer resp.Body.Close()

	// Drain the body to trigger nop's body stream logging and measure
	// the total download time.
	if _, err := io.Copy(files.body, resp.Body); err != nil {
		logger.Error(
			"sondaFailure",
			slog.String("operation", "readBody"),
			slog.Any("err", err),
			slog.Int("exitCode", 1),
		)
		return err
	}

	return nil
}
