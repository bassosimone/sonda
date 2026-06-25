// SPDX-License-Identifier: GPL-3.0-or-later

package measure

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"time"

	"github.com/bassosimone/closepool"
	"github.com/bassosimone/errclass"
	"github.com/bassosimone/nop"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
)

// httpMain is the main function of the `sonda measure http` subcommand.
func httpMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.Env

	// Set command defaults.
	var (
		bodyFile  = ""
		httpHost  = "1.1.1.1"
		method    = "GET"
		spanID    = nop.NewSpanID()
		target    = "1.1.1.1:80"
		timeout   = 30 * time.Second
		urlPath   = "/"
		userAgent = ""
	)

	// Honor SONDA_SPAN_ID environment variable.
	if v := env.Getenv("SONDA_SPAN_ID"); v != "" {
		spanID = v
	}

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda measure http", vflag.ExitOnError)
	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.Stdout
	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.StringVar(&bodyFile, 0, "body-file", "Save the response body to `FILE`. Empty means discard.")
	fset.StringVar(&httpHost, 0, "http-host", "Use `NAME` instead of `@DEFAULT_VALUE@`.")
	fset.StringVar(&method, 0, "method", "Use `METHOD` instead of `@DEFAULT_VALUE@`.")
	fset.StringVar(&spanID, 0, "span-id", "Use `ID` instead of a random one. Honors `SONDA_SPAN_ID`.")
	fset.StringVar(&target, 0, "target", "Use `ADDR:PORT` instead of `@DEFAULT_VALUE@`.")
	fset.DurationVar(&timeout, 0, "timeout", "Use `DURATION` instead of `@DEFAULT_VALUE@`.")
	fset.StringVar(&urlPath, 0, "url-path", "Use `PATH` instead of `@DEFAULT_VALUE@`.")
	fset.StringVar(&userAgent, 0, "user-agent", "Use `STRING`. Empty means no User-Agent header.")
	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Emit structured logs to the stdout tied together by a span ID.
	logger := slog.New(slog.NewJSONHandler(env.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	logger = logger.With("spanID", spanID)

	// Log the measurement start / done events.
	logger.Info("measurementStart")
	defer logger.Info("measurementDone")

	// Log the command line arguments for reproducibility.
	fullArgs := append([]string{"sonda", "measure", "http"}, args...)
	logger.Info("commandLineArgs", slog.Any("args", fullArgs))

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
	cfg.Dialer = env.Dialer
	cfg.ErrClassifier = nop.ErrClassifierFunc(errclass.New)

	// Create the dialing pipeline (TCP connect, plain HTTP).
	epntOp := nop.NewEndpointFunc(epnt)
	connectOp := nop.NewConnectFunc(cfg, "tcp", logger)
	observeOp := nop.NewObserveConnFunc(cfg, logger)
	autoCancelOp := nop.NewCancelWatchFunc()
	httpConnOp := nop.NewHTTPConnFuncPlain(cfg, logger)
	dialPipe := nop.Compose5(epntOp, connectOp, observeOp, autoCancelOp, httpConnOp)

	// Dial the HTTP connection.
	httpConn, err := dialPipe.Call(ctx, nop.Unit{})
	if err != nil {
		logger.Error("dialFailed", slog.Any("err", err))
		env.Exit(1)
	}
	defer httpConn.Close()

	// Build the HTTP request.
	httpURL := (&url.URL{Scheme: "http", Host: httpHost, Path: urlPath}).String()
	httpReq, err := http.NewRequestWithContext(ctx, method, httpURL, http.NoBody)
	if err != nil {
		logger.Error("newRequestFailed", slog.Any("err", err))
		env.Exit(1)
	}
	httpReq.Header.Set("User-Agent", userAgent)

	// Perform the HTTP round trip.
	resp, err := httpConn.RoundTrip(httpReq)
	if err != nil {
		logger.Error("roundTripFailed", slog.Any("err", err))
		env.Exit(1)
	}
	defer resp.Body.Close()

	// Determine where to write the body.
	closers := &closepool.Pool{}
	var bodyDst io.Writer = io.Discard
	if bodyFile != "" {
		filep, err := os.OpenFile(bodyFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0640)
		if err != nil {
			logger.Error("createBodyFileFailed", slog.Any("err", err))
			env.Exit(1)
		}
		closers.Add(filep)
		bodyDst = filep
	}

	// Drain the body to trigger nop's body stream logging and measure
	// the total download time.
	bodySize, err := io.Copy(bodyDst, resp.Body)
	if err != nil {
		logger.Error("readBodyFailed", slog.Any("err", err))
		env.Exit(1)
	}
	logger.Info("httpResponseBody", slog.Int64("size", bodySize))

	// Make sure we successfully closed the body file.
	if err := closers.Close(); err != nil {
		logger.Error("closeBodyFileFailed", slog.Any("err", err))
		env.Exit(1)
	}

	return nil
}
