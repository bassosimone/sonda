// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/cmd/internal/plugincommand"
	"github.com/bassosimone/sonda/internal/config"
	"github.com/bassosimone/sonda/internal/spool"
	"github.com/bassosimone/sonda/internal/spoolfs"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/sonda/internal/triggers"
	"github.com/bassosimone/vflag"
	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	plugincommand.Main(0, realMain)
}

// drainMaxLineSize is the maximum accepted line size.
const drainMaxLineSize = 1 << 19

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

	// Setup the watcher to watch the triggers directory.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Error("fsnotify.NewWatcher", slog.Any("err", err))
		env.Exit(1)
	}
	etlPtnopPromRunDir := filepath.Join(runDir, "etl-ptnop-prom")
	defer watcher.Close()
	if err := watcher.Add(etlPtnopPromRunDir); err != nil {
		logger.Error("watcher.Add", slog.Any("err", err))
		env.Exit(1)
	}

	// Open the root spool directory using the spool API.
	spoolRootDir, err := spoolfs.NewRootDir(env, spoolDir)
	if err != nil {
		logger.Error("spoolfs.NewRootDir", slog.Any("err", err))
		env.Exit(1)
	}

	// Run in the background and monitor the triggers dir.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	reg := prometheus.NewRegistry()
	metrics := newMetricsSet(reg)
	reg.MustRegister(newCgroupCollector(env, logger, sliceDir))
	wg := &sync.WaitGroup{}
	wg.Go(func() {
		logger.Info("started background goroutine to monitor triggers")
		processTriggersLoop(
			ctx,
			env,
			etlPtnopPromRunDir,
			logger,
			maxSpanAge,
			metrics,
			spoolRootDir,
			watcher,
		)
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

// processTriggersLoop is the loop that processes the triggers dir in a loop.
func processTriggersLoop(
	ctx context.Context,
	env *testable.Environ,
	etlPtnopPromRunDir string,
	logger *slog.Logger,
	maxSpanAge time.Duration,
	metrics *metricsSet,
	spoolRootDir spool.RootDir,
	watcher *fsnotify.Watcher,
) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		cutoff := time.Now().Add(-maxSpanAge)
		_ = processTriggersDir(
			context.WithoutCancel(ctx), // finish the pass on shutdown
			cutoff,
			env,
			etlPtnopPromRunDir,
			logger,
			metrics,
			spoolRootDir,
		)
		if !processWaitEvents(ctx, ticker, watcher) {
			break
		}
	}
}

// processWaitEvents blocks until a context, ticker, or watcher event occurs.
//
// Returns false when the caller should stop waiting for events.
func processWaitEvents(ctx context.Context, ticker *time.Ticker, watcher *fsnotify.Watcher) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			return true
		case err, ok := <-watcher.Errors:
			runtimex.Assert(!ok || errors.Is(err, fsnotify.ErrEventOverflow))
			return ok
		case event, ok := <-watcher.Events:
			if ok && event.Has(fsnotify.Create) {
				return true
			}
		}
	}
}

// processTriggersDir does a pass over `/run/sonda/etl-ptnop-prom`, processing
// and removing each trigger file, and generating metrics as a side effect.
func processTriggersDir(
	ctx context.Context,
	cutoff time.Time,
	env *testable.Environ,
	etlPtnopPromRunDir string,
	logger *slog.Logger,
	metrics *metricsSet,
	spoolRootDir spool.RootDir,
) error {
	logger.Info("processing triggers dir", slog.String("dir", etlPtnopPromRunDir))

	dentries, err := env.ReadDir(etlPtnopPromRunDir)
	if err != nil {
		logger.Error("env.ReadDir", slog.Any("err", err))
		return err
	}

	var errv []error
	for _, dentry := range dentries {
		err := processDentry(
			ctx,
			cutoff,
			dentry,
			env,
			etlPtnopPromRunDir,
			logger,
			metrics,
			spoolRootDir,
		)
		if err != nil {
			logger.Warn("processDentry", slog.Any("err", err))
			errv = append(errv, err)
			continue
		}
	}

	return errors.Join(errv...)
}

// processDentry drains a single dentry in the `/run/sonda/etl-ptnop-prom` dir.
func processDentry(
	ctx context.Context,
	cutoff time.Time,
	dentry fs.DirEntry,
	env *testable.Environ,
	etlPtnopPromRunDir string,
	logger *slog.Logger,
	metrics *metricsSet,
	spoolRootDir spool.RootDir,
) error {
	// 1. The dentry must be a regular `<UUIDv7>.jsonl` file.
	name, mode := dentry.Name(), dentry.Type()
	fullFilePath := filepath.Join(etlPtnopPromRunDir, name)
	if !mode.IsRegular() {
		return fmt.Errorf("not a regular file: %s", fullFilePath)
	}
	spanID, err := uuid.Parse(strings.TrimSuffix(name, ".jsonl"))
	if err != nil {
		return fmt.Errorf("not a UUID file name: %s: %w", fullFilePath, err)
	}
	if spanID.Version() != 7 {
		return fmt.Errorf("invalid UUID version: %s", fullFilePath)
	}

	// 2. From now on, we remove the file on exit/panic.
	defer env.Remove(fullFilePath)

	// 3. The file must have been created after the cutoff.
	sec, nsec := spanID.Time().UnixTime()
	ts := time.Unix(sec, nsec)
	if ts.Before(cutoff) {
		logger.Warn("skip old dentry", slog.String("dentry", fullFilePath))
		return nil
	}

	// 4. Play it safe and read the file line by line. We assume the file
	// is reasonably small, but it is best to write robust code here.
	filep, err := env.OpenFile(fullFilePath, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("env.OpenFile: %w", err)
	}
	defer filep.Close()
	scanner := bufio.NewScanner(filep)
	scanner.Buffer(nil, drainMaxLineSize)

	var errv []error
	for scanner.Scan() {
		// 4.1. Parse information about a created span and fail hard if
		// the parsing fails: it means a corrupt trigger file.
		var info triggers.CreatedSpan
		if err := json.Unmarshal(scanner.Bytes(), &info); err != nil {
			return fmt.Errorf("%s: %w", fullFilePath, err)
		}

		// 4.2. Otherwise process each span and collect errors to be
		// reported all together at the end of the processing.
		err := processSpanTrigger(ctx, cutoff, env, &info, logger, metrics, spoolRootDir)
		if err != nil {
			logger.Warn("cannot process span", slog.Any("err", err))
			errv = append(errv, err)
			continue
		}
	}

	// 5. Handle errors that may have occurred.
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner.Err: %w", err)
	}
	if len(errv) >= 1 {
		return errors.New("failed to process at least one span")
	}
	return nil
}

// processSpanTrigger processes a single created span.
func processSpanTrigger(
	ctx context.Context,
	cutoff time.Time,
	env *testable.Environ,
	info *triggers.CreatedSpan,
	logger *slog.Logger,
	metrics *metricsSet,
	spoolRootDir spool.RootDir,
) error {
	// 1. The span must be about "ptnop".
	if info.DataType != "ptnop" {
		logger.Info("skip unhandled data type", slog.String("dataType", info.DataType))
		return nil
	}

	// 2. Make sure the spanID is valid and created after the cutoff.
	spanDir, err := spool.OpenSpanDir(spoolRootDir, "ptnop", info.SpanID)
	if err != nil {
		return fmt.Errorf("spool.OpenSpanDir: %w", err)
	}
	if spanDir.Created().Before(cutoff) {
		logger.Info("skip old span", slog.String("spanID", info.SpanID))
		return nil
	}

	// 3. Update Prometheus metrics using the span `stdout.txt` entity content.
	numProcessed, err := updateMetrics(ctx, logger, metrics, spanDir)
	if err != nil {
		return fmt.Errorf("updateMetrics: %s: %w", info.SpanID, err)
	}
	logger.Info(
		"updateMetrics",
		slog.Int64("numEventsProcessed", numProcessed),
		slog.String("spanId", info.SpanID),
	)
	return nil
}
