// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/cmd/internal/plugincommand"
	"github.com/bassosimone/sonda/internal/config"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
)

func main() {
	plugincommand.Main(0, mainMain)
}

// maxLineSize bounds the size of a request line. Requests are small JSON objects,
// so this is generous even with many HTTP headers or tags.
const maxLineSize = 1 << 19

func mainMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Set the command defaults.
	presets := config.Defaults()
	configErr := config.ReadDefaultsInto(env, presets)
	var (
		maxConns    = int64(8)
		idleTimeout = time.Duration(presets.Serve.Ptnop.IdleTimeout)
		socketPath  = config.PtnopSocketPath
		spoolDir    = config.SpoolDir
	)

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda-serve-ptnop", vflag.ExitOnError)

	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.UsageStdout

	upr := vflag.NewDefaultUsagePrinter()
	fset.UsagePrinter = upr
	upr.AddDescription(
		"Accept connections at `--socket=<path>` and serve ptnop " +
			"measurement requests writing one span per request into `<spool-dir>/ptnop` " +
			"and one JSON response line per request to the socket.")

	fset.AutoHelp('h', "help", "Show this help message and exit.")

	fset.Int64Var(&maxConns, 0, "max-conns",
		"Refuse serving more than `N` concurrent conns.",
		"Default: @DEFAULT_VALUE@.")

	fset.DurationVar(&idleTimeout, 0, "idle-timeout",
		"Close the connection after waiting `DURATION` for I/O to occur on the "+
			"client connection.",
		"Default: @DEFAULT_VALUE@.")

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

	// Make sure the idle timeout makes sense.
	if idleTimeout <= 0 {
		logger.Error("invalid --idle-timeout", slog.Duration("idleTimeout", idleTimeout))
		env.Exit(1)
	}

	// Make the spoolDir absolute for robustness and select the data type dir.
	spoolDir, err := env.Abs(spoolDir)
	if err != nil {
		logger.Error("env.Abs", slog.Any("err", err))
		env.Exit(1)
	}
	ptnopSpoolDir := filepath.Join(spoolDir, "ptnop")

	// Create the listening socket.
	listener, err := env.ListenConfig.Listen(ctx, "unix", socketPath)
	if err != nil {
		logger.Error("env.ListenConfig", slog.Any("err", err))
		env.Exit(1)
	}
	stopCloser := context.AfterFunc(ctx, func() {
		listener.Close()
	})
	defer stopCloser()

	// Wait for background goroutines on exit.
	wg := &sync.WaitGroup{}
	defer func() {
		logger.Info("waiting for background goroutines to stop")
		wg.Wait()
	}()

	// Accept connections and spawn serving goroutines. Handle overload
	// by dropping incoming connections, like systemd does.
	activeConns := &atomic.Int64{}
	for {
		conn, err := listener.Accept()
		if err != nil {
			logger.Warn("listener.Accept", slog.Any("err", err))
			env.Exit(1)
		}

		cur := activeConns.Load()
		if overload := cur >= maxConns; overload {
			logger.Warn(
				"too many client connections",
				slog.Int64("cur", cur),
				slog.Int64("max", maxConns),
			)
			conn.Close()
			continue
		}

		activeConns.Add(1)
		wg.Go(func() {
			defer activeConns.Add(-1)
			serveConn(ctx, conn, env, idleTimeout, logger, ptnopSpoolDir)
		})
	}
}

func serveConn(
	ctx context.Context,
	conn net.Conn,
	env *testable.Environ,
	idleTimeout time.Duration,
	logger *slog.Logger,
	ptnopSpoolDir string,
) {
	// Close the connection when we leave the scope or the service-scoped context is done.
	defer conn.Close()
	stopCloser := context.AfterFunc(ctx, func() {
		conn.Close()
	})
	defer stopCloser()

	// Get the peer credentials.
	reader := &idleReader{conn: conn, timeout: idleTimeout}
	writer := &idleWriter{conn: conn, timeout: idleTimeout}
	peer := unknownPeerCreds
	if uconn, ok := conn.(*net.UnixConn); ok {
		peer, _ = peerCred(uconn) // best effort + portability on Windows/macOS
	}

	// Read and serve incoming requests, one per line, sequentially.
	//
	// Each request receives exactly one response line. A malformed or invalid
	// request receives an error response and does not close the connection.
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(nil, maxLineSize)
	for scanner.Scan() {
		resp := serveLine(ctx, env, logger, scanner.Bytes(), ptnopSpoolDir, peer)
		respData := runtimex.PanicOnError1(json.Marshal(resp)) // always serializable
		respData = append(respData, '\n')
		if _, err := writer.Write(respData); err != nil {
			logger.Warn("writer.Write", slog.Any("err", err))
			return // the client is gone or not reading
		}
	}

	// Note: an idle client or a line longer than maxLineSize ends the connection here.
	if err := scanner.Err(); err != nil {
		logger.Warn("scanner.Err", slog.Any("err", err))
	}
}
