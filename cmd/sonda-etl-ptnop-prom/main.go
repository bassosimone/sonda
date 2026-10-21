// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/cmd/internal/plugincommand"
	"github.com/bassosimone/sonda/internal/config"
	"github.com/bassosimone/sonda/internal/spool"
	"github.com/bassosimone/sonda/internal/spoolfs"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/sonda/internal/triggersreader"
	"github.com/bassosimone/vflag"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	plugincommand.Main(0, realMain)
}

// validHost maps valid host names to true.
var validHost = map[string]bool{
	"localhost": true,
	"127.0.0.1": true,
	"::1":       true,
}

// validateHostHandler ensures that the host is localhost-adjacent so that a DNS rebinding
// attack does not allow a browser to reach out to our service.
func validateHostHandler(mux http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if !validHost[req.Host] {
			addr, _, err := net.SplitHostPort(req.Host)
			if err != nil || !validHost[addr] {
				rw.WriteHeader(http.StatusMisdirectedRequest)
				return
			}
			// fallthrough
		}
		mux.ServeHTTP(rw, req)
	})
}

func realMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Set the command defaults.
	var (
		httpEpnt   = "127.0.0.1:9774"
		maxSpanAge = 24 * time.Hour
		sliceDir   = cgroupDefaultSliceDir
		spoolDir   = config.SpoolDir
		runDir     = config.RunDir
	)

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda-etl-ptnop-prom", vflag.ExitOnError)

	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.UsageStdout

	upr := vflag.NewDefaultUsagePrinter()
	fset.UsagePrinter = upr
	upr.AddDescription(
		"Export ptnop logs as Prometheus metrics.",
		"Read triggers from `$runDir/etl-ptnop-prom`.",
		"Read the corresponding ptnop data from `$spoolDir/ptnop`.",
		"Export metrics at `--http <endpoint>`.",
	)

	fset.AutoHelp('h', "help", "Show this help message and exit.")

	fset.StringVar(&httpEpnt, 0, "http",
		"Export Prometheus metrics at the given `EPNT`.",
		"Default: `@DEFAULT_VALUE@`.")

	fset.DurationVar(&maxSpanAge, 0, "max-span-age",
		"Ignore spans older than `DURATION`.")

	fset.StringVar(&runDir, 0, "run-dir",
		"Top-level run `DIR` containing the trigger directories.",
		"Default: `@DEFAULT_VALUE@`.")

	fset.StringVar(&spoolDir, 0, "spool-dir",
		"Top-level spool `DIR` containing the data type directories.",
		"Default: `@DEFAULT_VALUE@`.")

	fset.StringVar(&sliceDir, 0, "slice-dir",
		"Read the slice resource usage from the cgroup v2 `DIR`.",
		"Default: `@DEFAULT_VALUE@`.")

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using ExitOnError

	// Emit structured logs to stderr.
	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))

	// Open the root spool dir using the spool API.
	spoolRootDir, err := spoolfs.NewRootDir(env, spoolDir)
	if err != nil {
		logger.Error("spoolfs.NewRootDir", slog.Any("err", err))
		env.Exit(1)
	}

	// Initialize the reading loop.
	loop, err := triggersreader.NewLoop(
		"ptnop", // dataType
		env,
		logger,
		maxSpanAge,
		filepath.Join(runDir, "etl-ptnop-prom"),
		spoolRootDir,
	)
	if err != nil {
		logger.Error("triggersreader.NewLoop", slog.Any("err", err))
		env.Exit(1)
	}
	defer loop.Close()

	// Run in the background and monitor the triggers dir.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	reg := prometheus.NewRegistry()
	metrics := newMetricsSet(reg)
	reg.MustRegister(newCgroupCollector(env, logger, sliceDir))
	wg := &sync.WaitGroup{}
	wg.Go(func() {
		logger.Info("started background goroutine to monitor triggers")
		loop.Run(
			ctx,
			func(spanDir spool.SpanDir) error {
				numProcessed, err := updateMetrics(logger, metrics, spanDir)
				if err != nil {
					return fmt.Errorf("updateMetrics: %s: %w", spanDir.ID(), err)
				}
				logger.Info(
					"updateMetrics",
					slog.Int64("numEventsProcessed", numProcessed),
					slog.String("spanId", spanDir.ID()),
				)
				return nil
			},
			func(cutoff time.Time) error {
				return nil // no maintenance required by this ETL pipeline
			})
	})

	// Create the listener for HTTP
	listener, err := env.ListenConfig.Listen(ctx, "tcp", httpEpnt)
	if err != nil {
		logger.Error("env.ListenConfig.Listen", slog.Any("err", err))
		env.Exit(1)
	}
	logger.Info("listening", slog.String("addr", httpEpnt))

	// Setup the HTTP mux exporting metrics and the server
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	srvr := &http.Server{
		Handler:             validateHostHandler(mux),
		ReadTimeout:         30 * time.Second,
		ReadHeaderTimeout:   30 * time.Second,
		WriteTimeout:        30 * time.Second,
		IdleTimeout:         30 * time.Second,
		MaxHeaderBytes:      1 << 20,
		MaxHeaderValueCount: 1 << 10,
	}
	stop := context.AfterFunc(ctx, func() {
		srvr.Close()
	})
	defer stop()

	// Serve and handle the case where the server is closed
	err = srvr.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	if err != nil {
		logger.Error("srvr.Serve", slog.Any("err", err))
		env.Exit(1)
	}

	// Stop listening and wait for all background goroutines to stop.
	listener.Close()
	logger.Info("waiting for background goroutines to terminate")
	wg.Wait()
	return nil
}
