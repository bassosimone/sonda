// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/cmd/internal/plugincommand"
	"github.com/bassosimone/sonda/internal/config"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
)

// maxLineSize bounds the size of a request line. Requests are small JSON objects,
// so this is generous even with many HTTP headers or tags.
const maxLineSize = 1 << 19

func mainMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Set the command defaults.
	presets := config.Defaults()
	configErr := config.ReadInto(env, config.DefaultConfigFilePath, presets)
	var (
		idleTimeout = time.Duration(presets.Inetd.Ptnop.IdleTimeout)
		spoolDir    = config.SpoolDir
		stdio       = false
	)

	// Parse command line flags.
	//
	// Note that the socket is attached to the stdin and the stdout, while
	// the systemd unit is expected to send the stderr to the journal.
	fset := vflag.NewFlagSet("sonda-inetd-ptnop", vflag.ExitOnError)

	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.UsageStdout

	upr := vflag.NewDefaultUsagePrinter()
	fset.UsagePrinter = upr
	upr.AddDescription(
		"Serve ptnop measurement requests read as JSON lines from the stdin, "+
			"writing one span per request into `<spool-dir>/ptnop` and one JSON "+
			"response line per request to the stdout.",
		"Designed to run behind a systemd socket with: ",
		"    - Accept=yes",
		"    - StandardError=journal",
		"    - StandardInput=socket",
		"which attaches the connection to stdin and stdout and "+
			"sends logs to the journal. The code assumes that the stdin "+
			"is a socket unless `--stdio` is given; use it for manual testing.")

	upr.AddExamples(
		"Serve a Unix socket without installing the systemd units, using "+
			"a socket path that is absolute and shorter than 108 bytes:",
		"    systemd-socket-activate --listen=/tmp/ptnop.sock --accept --inetd \\\n"+
			"          sonda-inetd-ptnop --spool-dir /tmp/spool",
		"Connect to the socket using:",
		"    nc -U /tmp/ptnop.sock")

	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.DurationVar(&idleTimeout, 0, "idle-timeout",
		"Close the connection after waiting `DURATION` for I/O to occur on the "+
			"client connection. Ignored with `--stdio`.",
		"Default: @DEFAULT_VALUE@.")
	fset.StringVar(&spoolDir, 0, "spool-dir",
		"Top-level spool `DIR` containing the data type directories.",
		"Default: `@DEFAULT_VALUE@`.")
	fset.BoolVar(&stdio, 0, "stdio", "Do not assume that the stdin is a socket.")

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Emit operational logs to the stderr (i.e., the journal).
	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))

	// Defer reporting config errors after flag parsing to honor `-h/--help`.
	if configErr != nil {
		logger.Error("config.Read", slog.Any("err", configErr))
		env.Exit(1)
	}

	// Make sure the idle timeout makes sense.
	if !stdio && idleTimeout <= 0 {
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

	// Select the transport.
	//
	// By default, the stdin must be a socket. We wrap it as a [net.Conn] so that
	// we can close the connection with idle clients, which would otherwise hold
	// one of the socket unit's MaxConnections slots forever. With `--stdio`, any
	// stdin works, but an idle client can block us forever.
	//
	// Note: once we wrap the socket, we MUST NOT use the stdout anymore, since
	// [net.FileConn] makes the open file description non-blocking and, under
	// systemd, the stdin and the stdout share the same file description.
	var (
		peer   peerCreds = unknownPeerCreds
		reader io.Reader = env.Stdin
		writer io.Writer = env.Stdout
	)
	if !stdio {
		conn, err := env.FileConn(os.Stdin)
		if err != nil {
			logger.Error("env.FileConn", slog.Any("err", err))
			env.Exit(1)
		}
		defer conn.Close()
		reader = &idleReader{conn: conn, timeout: idleTimeout}
		writer = &idleWriter{conn: conn, timeout: idleTimeout}

		// Obtain the peer credentials once, since they belong to the connection.
		//
		// We only try with Unix domain sockets: on a TCP socket, Linux does not fail
		// SO_PEERCRED but returns pid 0 and uid/gid 4294967295 (tested on 7.0.0).
		// Failing is fine: we log and continue without credentials.
		if uconn, ok := conn.(*net.UnixConn); ok {
			var err error
			peer, err = peerCred(uconn)
			if err != nil {
				logger.Warn("peerCred", slog.Any("err", err))
			}
		}
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
			return nil // the client is gone or not reading
		}
	}

	// Note: an idle client or a line longer than maxLineSize ends the connection here.
	if err := scanner.Err(); err != nil {
		logger.Warn("scanner.Err", slog.Any("err", err))
	}
	return nil
}

func main() {
	plugincommand.Main(0, mainMain)
}
