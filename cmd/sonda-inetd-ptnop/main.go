// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"

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
		"Serve ptnop measurement requests read as JSON lines from the stdin, " +
			"writing one span per request into `--spool-dir` and one JSON " +
			"response line per request to the stdout. Designed to run behind " +
			"a systemd socket with `Accept=yes`, `StandardInput=socket`, and " +
			"`StandardError=journal`, which attaches the connection " +
			"to the stdin and the stdout and sends logs to the journal.")

	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.StringVar(&spoolDir, 0, "spool-dir", "Use `DIR` instead of `@DEFAULT_VALUE@`.")

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Emit operational logs to the stderr (i.e., the journal).
	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))

	// Make the spoolDir absolute for robustness.
	spoolDir, err := env.Abs(spoolDir)
	if err != nil {
		logger.Error("env.Abs", slog.Any("err", err))
		env.Exit(1)
	}

	// Read and serve incoming requests, one per line, sequentially.
	//
	// Each request receives exactly one response line. A malformed or invalid
	// request receives an error response and does not close the connection.
	scanner := bufio.NewScanner(env.Stdin)
	scanner.Buffer(nil, maxLineSize)
	for scanner.Scan() {
		resp := serveLine(ctx, env, logger, scanner.Bytes(), spoolDir)
		respData := runtimex.PanicOnError1(json.Marshal(resp)) // always serializable
		respData = append(respData, '\n')
		if _, err := env.Stdout.Write(respData); err != nil {
			logger.Warn("env.Stdout.Write", slog.Any("err", err))
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
