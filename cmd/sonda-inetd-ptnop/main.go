// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/cmd/internal/plugincommand"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
)

// maxLineSize bounds the size of a request line. Requests are small JSON objects,
// so this is generous even with many HTTP headers or tags.
const maxLineSize = 1 << 19

func mainMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Set command defaults.
	var (
		spoolDir = "."
		stdio    = false
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
			"writing one span per request into the `--spool-dir` dir and one JSON "+
			"response line per request to the stdout.",
		"Designed to run behind a systemd socket with: ",
		"    - Accept=yes",
		"    - StandardError=journal",
		"    - StandardInput=socket",
		"which attaches the connection to stdin and stdout and "+
			"sends logs to the journal. The code assumes that the stdin "+
			"is a socket unless `--stdio` is given; use it for manual testing.")

	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.StringVar(&spoolDir, 0, "spool-dir", "Use `DIR` instead of `@DEFAULT_VALUE@`.")
	fset.BoolVar(&stdio, 0, "stdio", "Do not assume that the stdin is a socket.")

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Emit operational logs to the stderr (i.e., the journal).
	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))

	// Make the spoolDir absolute for robustness.
	spoolDir, err := env.Abs(spoolDir)
	if err != nil {
		logger.Error("env.Abs", slog.Any("err", err))
		env.Exit(1)
	}

	// Select the transport.
	//
	// By default, the stdin must be a socket. We wrap it as a [net.Conn], which
	// will later allow us to set deadlines. With `--stdio`, any stdin works.
	//
	// Note: once we wrap the socket, we MUST NOT use the stdout anymore, since
	// [net.FileConn] makes the open file description non-blocking and, under
	// systemd, the stdin and the stdout share the same file description.
	var (
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
		reader, writer = conn, conn
	}

	// Read and serve incoming requests, one per line, sequentially.
	//
	// Each request receives exactly one response line. A malformed or invalid
	// request receives an error response and does not close the connection.
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(nil, maxLineSize)
	for scanner.Scan() {
		resp := serveLine(ctx, env, logger, scanner.Bytes(), spoolDir)
		respData := runtimex.PanicOnError1(json.Marshal(resp)) // always serializable
		respData = append(respData, '\n')
		if _, err := writer.Write(respData); err != nil {
			logger.Warn("writer.Write", slog.Any("err", err))
			return nil // the client is gone
		}
	}

	// Note: a line longer than maxLineSize ends the connection here.
	if err := scanner.Err(); err != nil {
		logger.Warn("scanner.Err", slog.Any("err", err))
	}
	return nil
}

func main() {
	plugincommand.Main(0, mainMain)
}
