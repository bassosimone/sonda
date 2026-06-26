// SPDX-License-Identifier: GPL-3.0-or-later

package measure

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/bassosimone/closepool"
	"github.com/bassosimone/errclass"
	"github.com/bassosimone/nop"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
)

// httpsMain is the main function of the `sonda measure https` subcommand.
func httpsMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.Env

	// Set command defaults.
	var (
		bodyFile  = ""
		headers   []string
		httpHost  = "1.1.1.1"
		method    = "GET"
		sni       = "1.1.1.1"
		spanID    = nop.NewSpanID()
		target    = "1.1.1.1:443"
		timeout   = 30 * time.Second
		urlPath   = "/"
	)

	// Honor SONDA_SPAN_ID environment variable.
	if v := env.Getenv("SONDA_SPAN_ID"); v != "" {
		spanID = v
	}

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda measure https", vflag.ExitOnError)
	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.Stdout
	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.StringVar(&bodyFile, 0, "body-file", "Save the response body to `FILE`. Empty means discard.")
	fset.StringSliceVar(&headers, 'H', "header", "Add `KEY: VALUE` request header. Repeatable.")
	fset.StringVar(&httpHost, 0, "http-host", "Use `NAME` instead of `@DEFAULT_VALUE@`.")
	fset.StringVar(&method, 0, "method", "Use `METHOD` instead of `@DEFAULT_VALUE@`.")
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
	fullArgs := append([]string{"sonda", "measure", "https"}, args...)
	logger.Info("sondaCommandLineArgs", slog.Any("cliArgs", fullArgs))

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
		logger.Error("sondaFailure", slog.String("operation", "dial"), slog.Any("err", err))
		env.Exit(1)
	}
	defer httpConn.Close()

	// Build the HTTP request.
	httpURL := (&url.URL{Scheme: "https", Host: httpHost, Path: urlPath}).String()
	httpReq, err := http.NewRequestWithContext(ctx, method, httpURL, http.NoBody)
	if err != nil {
		logger.Error("sondaFailure", slog.String("operation", "newRequest"), slog.Any("err", err))
		env.Exit(1)
	}
	for _, h := range headers {
		key, value, ok := strings.Cut(h, ":")
		if !ok {
			logger.Error("sondaFailure", slog.String("operation", "parseHeader"), slog.String("err", "missing colon"))
			fmt.Fprintf(env.Stderr, "sonda measure https: invalid header (missing ':'): %s\n", h)
			env.Exit(2)
		}
		httpReq.Header.Add(strings.TrimSpace(key), strings.TrimSpace(value))
	}

	// Perform the HTTP round trip.
	resp, err := httpConn.RoundTrip(httpReq)
	if err != nil {
		logger.Error("sondaFailure", slog.String("operation", "roundTrip"), slog.Any("err", err))
		env.Exit(1)
	}
	defer resp.Body.Close()

	// Determine where to write the body.
	closers := &closepool.Pool{}
	var bodyDst io.Writer = io.Discard
	if bodyFile != "" {
		filep, err := os.OpenFile(bodyFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0640)
		if err != nil {
			logger.Error("sondaFailure", slog.String("operation", "createBodyFile"), slog.Any("err", err))
			env.Exit(1)
		}
		closers.Add(filep)
		bodyDst = filep
	}

	// Drain the body to trigger nop's body stream logging and measure
	// the total download time.
	bodySize, err := io.Copy(bodyDst, resp.Body)
	if err != nil {
		logger.Error("sondaFailure", slog.String("operation", "readBody"), slog.Any("err", err))
		env.Exit(1)
	}
	logger.Info("sondaHttpResponseBodyStats", slog.Int64("httpResponseBodySize", bodySize))

	// Make sure we successfully closed the body file.
	if err := closers.Close(); err != nil {
		logger.Error("sondaFailure", slog.String("operation", "closeBodyFile"), slog.Any("err", err))
		env.Exit(1)
	}

	return nil
}
