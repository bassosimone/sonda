// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/bassosimone/deferexit"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/plugins/nob"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vclip"
	"github.com/bassosimone/vflag"
)

// Forward declaration from the importable [nob] package.
const maxRequestBodySize = nob.MaxBodySize

func main() {
	// Transform panics into [os.Exit] calls.
	defer deferexit.Recover(os.Exit)
	env := testable.Env

	// Wrap the actual main dispatcher using `vclip.RootCommand`.
	root := vclip.NewRootCommand(vclip.CommandFunc(realMain))
	root.LogFatalOnError0 = env.LogFatalOnError0

	// Execute the dispatcher command wrapper.
	root.Main(context.Background(), env.Args[1:])
}

func realMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Set command defaults.
	var (
		spoolDir   = "."        // --spool-dir <dir>
		socketPath = "nob.sock" // --socket <path>
	)

	// TODO(bassosimone): consider socket activation here and otherwise deal with
	// the socket permissions and with unlinking it when done.

	// TODO(bassosimone): the socket is a new privilege boundary and we need to
	// add proper documentation around this for code reviews by agents.

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda-nobd", vflag.ExitOnError)

	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.UsageStdout

	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.StringVar(&spoolDir, 0, "spool-dir",
		"Use `DIR` as spool directory.",
		"Default: `@DEFAULT_VALUE@`.")
	fset.StringVar(&socketPath, 0, "socket",
		"Use `PATH` as the Unix socket path.",
		"Default: `@DEFAULT_VALUE@`.")

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Create structured logger.
	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))

	// Start listening on the Unix socket.
	listener, err := env.ListenConfig.Listen(ctx, "unix", socketPath)
	if err != nil {
		logger.Error("listen", slog.String("socketPath", socketPath), slog.Any("err", err))
		env.Exit(1)
	}
	defer listener.Close()
	logger.Info("listening", slog.String("socketPath", socketPath))

	// TODO(bassosimone): consider limiting concurrency using a semaphore
	// here so that we can return `429` when needed.

	// TODO(bassosimone): document that a client disconnecting mid
	// measurement causes the measurement to be aborted.

	// Create and initialize the HTTP mux.
	handler := &handler{
		dir:    spoolDir,
		env:    env,
		logger: logger,
	}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/spans/{spanID}/{fileName}", http.HandlerFunc(handler.GetSpanFile))
	mux.Handle("POST /api/v1/run", http.HandlerFunc(handler.Run))
	mux.Handle("POST /api/v1/gc", http.HandlerFunc(handler.GC))

	// Create the HTTP server.
	srvr := &http.Server{Handler: mux}
	context.AfterFunc(ctx, func() {
		srvr.Close()
	})

	// Serve requests until we're interrupted.
	err = srvr.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("serving", slog.String("socketPath", socketPath), slog.Any("err", err))
		env.Exit(1)
	}
	logger.Info("serving", slog.String("socketPath", socketPath), slog.Any("err", err))
	return nil
}

// handler handles requests relative to `dir`.
type handler struct {
	// dir is the spool directory.
	dir string

	// env allows mocking dependencies.
	env *testable.Environ

	// logger is the logger to use.
	logger *slog.Logger
}
