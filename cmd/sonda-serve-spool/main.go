// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/cmd/internal/plugincommand"
	"github.com/bassosimone/sonda/internal/config"
	"github.com/bassosimone/sonda/internal/spoolserver"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
)

func main() {
	plugincommand.Main(0, mainMain)
}

func mainMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Set the command defaults.
	presets := config.Defaults()
	configErr := config.ReadDefaultsInto(env, presets)
	var (
		maxMem     = uint64(spoolserver.DefaultMaxMemory)
		socketPath = config.SpoolSocketPath
		spoolDir   = config.SpoolDir
	)

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda-serve-spool", vflag.ExitOnError)

	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.UsageStdout

	upr := vflag.NewDefaultUsagePrinter()
	fset.UsagePrinter = upr
	upr.AddDescription(
		"Memory-based spool implementation. Other sonda components use " +
			"`--socket` to communicate with this service. Can also " +
			"read and serve the `--spool-dir` data.")

	fset.AutoHelp('h', "help", "Show this help message and exit.")

	fset.Uint64Var(&maxMem, 0, "max-memory",
		"Set maximum memory for the span entity cache to `N` bytes.",
		"Default: `@DEFAULT_VALUE@`.")

	fset.StringVar(&socketPath, 0, "socket",
		"Unix socket `PATH` to listen at.",
		"Default: `@DEFAULT_VALUE@`.")

	fset.StringVar(&spoolDir, 0, "spool-dir",
		"Top-level spool `DIR` containing the data type directories.",
		"Default: `@DEFAULT_VALUE@`.")

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Emit operational logs to the stderr (i.e., the journal).
	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))

	// Defer reporting config errors after flag parsing to honor `-h/--help`.
	if configErr != nil {
		logger.Error("config.ReadDefaultsInto", slog.Any("err", configErr))
		env.Exit(1)
	}

	// Create the mux.
	mux := http.NewServeMux()
	handler := spoolserver.NewHandler(env, logger, spoolDir)
	handler.MaxMemory = maxMem
	handler.Register(mux)

	// Create the HTTP server.
	srvr := &http.Server{
		Handler:             mux,
		ReadTimeout:         60 * time.Second,
		ReadHeaderTimeout:   60 * time.Second,
		WriteTimeout:        60 * time.Second,
		IdleTimeout:         60 * time.Second,
		MaxHeaderBytes:      1 << 20,
		MaxHeaderValueCount: 1024,
	}
	stopCloser := context.AfterFunc(ctx, func() {
		srvr.Close()
	})
	defer stopCloser()

	// Create the listening socket.
	listener, err := env.ListenConfig.Listen(ctx, "unix", socketPath)
	if err != nil {
		logger.Error("env.ListenConfig", slog.Any("err", err))
		env.Exit(1)
	}

	// Serve HTTP requests.
	err = srvr.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	if err != nil {
		logger.Error("srvr.Serve", slog.Any("err", err))
		env.Exit(1)
	}
	return nil
}
