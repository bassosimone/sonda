// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/cmd/internal/plugincommand"
	"github.com/bassosimone/sonda/internal/config"
	"github.com/bassosimone/sonda/internal/ptnoprpc"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
)

func main() {
	plugincommand.Main(0, realMain)
}

const shortDescr = "Run a measurement pipeline using the ptnop engine."

func realMain(ctx context.Context, args []string) error {
	// 1. Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// 2. Parse command line flags.
	opts := &ptnoprpc.Request{
		Timeout: 30 * time.Second,
	}
	var (
		dryRun      = false
		ptnopSocket = config.PtnopSocketPath
	)
	fset := vflag.NewFlagSet("sonda-measure-ptnop", vflag.ExitOnError)

	fset.Exit = env.Exit
	fset.Stdout = env.UsageStdout
	fset.Stderr = env.Stderr

	upr := vflag.NewDefaultUsagePrinter()
	fset.UsagePrinter = upr
	upr.AddDescription(shortDescr,
		"The measurement runs in the remote `sonda-inetd-ptnop` process, with "+
			"which we communicate using a Unix domain socket.",
		"Output",
		"    NDJSON. The first line is a `sondaRemoteMeasurementResult` event containing:",
		"        exitCode",
		"            remote measurement exit code",
		"        spanDir",
		"            measurement path within the spool dir",
		"        spanId",
		"            measurement span ID",
		"    The following lines copy the remote structured logs, after the measurement completes.",
		"Pipeline",
		"    dns-over-https",
		"        Requires: `--addr-port <addrport>`",
		"        Requires: `--sni <host>`",
		"        Requires: `--http-method POST`",
		"        Requires: `--http-scheme https`",
		"        Requires: `--http-host <host>`",
		"        Requires: `--url-path <path>`",
		"        Requires: `--dns-query-name <name>`",
		"        Requires: `--dns-query-type <type>`",
		"        Suggests: `--alpn h2 --alpn http/1.1`",
		"    dns-over-tcp",
		"        Requires: `--addr-port <addrport>`",
		"        Requires: `--dns-query-name <name>`",
		"        Requires: `--dns-query-type <type>`",
		"    dns-over-tls",
		"        Requires: `--addr-port <addrport>`",
		"        Requires: `--sni <host>`",
		"        Requires: `--dns-query-name <name>`",
		"        Requires: `--dns-query-type <type>`",
		"        Suggests: `--alpn dot`",
		"    dns-over-udp",
		"        Requires: `--addr-port <addrport>`",
		"        Requires: `--dns-query-name <name>`",
		"        Requires: `--dns-query-type <type>`",
		"    http",
		"        Requires: `--addr-port <addrport>`",
		"        Requires: `--http-method <method>`",
		"        Requires: `--http-scheme http`",
		"        Requires: `--http-host <host>`",
		"        Requires: `--url-path <path>`",
		"        Suggests: `--http-header <key:value> ...`",
		"        Suggests: `--http-body-file <path>`",
		"    https",
		"        Requires: `--addr-port <addrport>`",
		"        Requires: `--sni <host>`",
		"        Requires: `--http-method <method>`",
		"        Requires: `--http-scheme https`",
		"        Requires: `--http-host <host>`",
		"        Requires: `--url-path <path>`",
		"        Suggests: `--http-header <key:value> ...`",
		"        Suggests: `--http-body-file <path>`",
		"        Suggests: `--alpn h2 --alpn http/1.1`",
		"    tcp",
		"        Requires: `--addr-port <addrport>`",
		"    tls",
		"        Requires: `--addr-port <addrport>`",
		"        Requires: `--sni <host>`",
		"        Suggests: `--alpn <alpn> ...`",
		"    stun",
		"        Requires: `--addr-port <addrport>`",
		"Exit Code",
		"    0 (success)",
		"    1 (measurement error)",
		"    2 (usage error)",
		"    3 (unix socket dial error)",
		"    4 (any other error)")

	upr.AddExamples(
		"dns-over-https",
		"    sonda-measure-ptnop \\\n"+
			"          --pipeline dns-over-https \\\n"+
			"          --addr-port 8.8.8.8:443 \\\n"+
			"          --sni dns.google \\\n"+
			"          --http-method POST \\\n"+
			"          --http-scheme https \\\n"+
			"          --http-host dns.google \\\n"+
			"          --url-path /dns-query \\\n"+
			"          --dns-query-name www.example.com \\\n"+
			"          --dns-query-type A \\\n"+
			"          --alpn h2 --alpn http/1.1",
		"dns-over-tcp",
		"    sonda-measure-ptnop \\\n"+
			"          --pipeline dns-over-tcp \\\n"+
			"          --addr-port 8.8.8.8:53 \\\n"+
			"          --dns-query-name www.example.com \\\n"+
			"          --dns-query-type A",
		"dns-over-tls",
		"    sonda-measure-ptnop \\\n"+
			"          --pipeline dns-over-tls \\\n"+
			"          --addr-port 8.8.8.8:853 \\\n"+
			"          --sni dns.google \\\n"+
			"          --dns-query-name www.example.com \\\n"+
			"          --dns-query-type A \\\n"+
			"          --alpn dot",
		"dns-over-udp",
		"    sonda-measure-ptnop \\\n"+
			"          --pipeline dns-over-udp \\\n"+
			"          --addr-port 8.8.8.8:53 \\\n"+
			"          --dns-query-name www.example.com \\\n"+
			"          --dns-query-type A",
		"http",
		"    sonda-measure-ptnop \\\n"+
			"          --pipeline http \\\n"+
			"          --addr-port 1.1.1.1:80 \\\n"+
			"          --http-method GET \\\n"+
			"          --http-scheme http \\\n"+
			"          --http-host 1.1.1.1 \\\n"+
			"          --url-path /",
		"https",
		"    sonda-measure-ptnop \\\n"+
			"          --pipeline https \\\n"+
			"          --addr-port 1.1.1.1:443 \\\n"+
			"          --sni 1.1.1.1 \\\n"+
			"          --http-method GET \\\n"+
			"          --http-scheme https \\\n"+
			"          --http-host 1.1.1.1 \\\n"+
			"          --url-path / \\\n"+
			"          --alpn h2 --alpn http/1.1",
		"tcp",
		"    sonda-measure-ptnop \\\n"+
			"          --pipeline tcp \\\n"+
			"          --addr-port 1.1.1.1:443",
		"tls",
		"    sonda-measure-ptnop \\\n"+
			"          --pipeline tls \\\n"+
			"          --addr-port 1.1.1.1:443 \\\n"+
			"          --sni 1.1.1.1 \\\n"+
			"          --alpn h2 --alpn http/1.1",
		"stun",
		"    sonda-measure-ptnop \\\n"+
			"          --pipeline stun \\\n"+
			"          --addr-port 74.125.250.129:19302",
	)

	fset.BoolVar(&dryRun, 'n', "dry-run", "Print RPC request to the stdout and exit.")

	fset.StringSliceVar(&opts.ALPN, 0, "alpn",
		"Negotiate the given `PROTO` using the TLS ALPN extension.",
		"Repeat to negotiate multiple protocols.")

	fset.StringVar(&opts.AddrPort, 0, "addr-port",
		"Connect to the transport endpoint at `ADDRPORT`.",
		"Quote IPv6 addresses using `[` and `]`.")

	fset.StringVar(&opts.DNSQueryName, 0, "dns-query-name",
		"Use the given DNS query `NAME`.")

	fset.StringVar(&opts.DNSQueryType, 0, "dns-query-type",
		"Use the given DNS query `TYPE` (e.g., A, AAAA).")

	fset.AutoHelp('h', "help", "Show this help message and exit.")

	fset.BoolVar(&opts.HTTPBodyFile, 0, "http-body-file",
		"Save the HTTP response body inside the spool dir.",
		"Default: @DEFAULT_VALUE@.")

	fset.StringSliceVar(&opts.HTTPHeaders, 0, "http-header",
		"Send the given HTTP request `HEADER` (`KEY: VALUE`).",
		"Repeat to set additional headers.")

	fset.StringVar(&opts.HTTPHost, 0, "http-host",
		"Use `HOST` as the host header value.")

	fset.StringVar(&opts.HTTPMethod, 0, "http-method",
		"Use `METHOD` as the request method.")

	fset.StringVar(&opts.HTTPScheme, 0, "http-scheme",
		"Use `SCHEME` as the URL scheme.")

	fset.StringVar(&opts.Pipeline, 0, "pipeline",
		"Use `PIPELINE` as the measurement pipeline.")

	fset.StringVar(&ptnopSocket, 0, "ptnop-socket",
		"Unix domain socket `PATH` of the `sonda-inetd-ptnop` server.",
		"Default: `@DEFAULT_VALUE@`.")

	fset.StringVar(&opts.SNI, 0, "sni",
		"Use `HOST` in the TLS SNI extension.")

	fset.StringSliceVar(&opts.Tags, 0, "tag",
		"Annotate the structured logs with the given `KEY=VALUE` tag.",
		"Repeat to annotate the logs with multiple tags.")

	fset.DurationVar(&opts.Timeout, 0, "timeout",
		"Timeout the measurement after `DURATION`.",
		"Default value: `@DEFAULT_VALUE@`.")

	fset.StringVar(&opts.URLPath, 0, "url-path",
		"Use the given `PATH` as the URL path.")

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// If the user provided no arguments, print the help.
	if len(args) <= 0 {
		fset.PrintUsageString(fset.Stdout)
		env.Exit(0)
	}

	// 3. Honor the `-n/--dry-run` flag.
	if dryRun {
		fmt.Fprintf(env.Stdout, "%s\n", runtimex.PanicOnError1(json.Marshal(opts)))
		env.Exit(0)
	}

	// 4. Create the structured logger.
	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))

	// 5. Refuse to run inside `sonda spool run`.
	if env.Getenv("SONDA_SPAN_ID") != "" {
		logger.Error("cannot run inside `sonda spool run`")
		env.Exit(2)
	}

	// 6. Create the connection with `sonda-inetd-ptnop`.
	conn, err := ptnoprpc.Dial(ctx, env, ptnopSocket)
	if err != nil {
		logger.Error("ptnoprpc.Dial", slog.Any("err", err))
		env.Exit(3)
	}
	defer conn.Close()

	// 7. Send the request and receive the response.
	spanDir, err := conn.Run(ctx, opts)
	if err != nil {
		logger.Error("conn.Run", slog.Any("err", err))
		if _, ok := errors.AsType[ptnoprpc.UsageError](err); ok {
			env.Exit(2)
		}
		env.Exit(4)
	}

	// 8. Print the response to the stdout.
	//
	// Like `sonda-inetd-ptnop`, we omit the top-level time field, so that this
	// line has the same shape as the structured logs we copy below.
	stdoutLogger := slog.New(slog.NewJSONHandler(env.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey && len(groups) <= 0 {
				return slog.Attr{}
			}
			return attr
		},
	}))
	stdoutLogger.Info(
		"sondaRemoteMeasurementResult",
		slog.Int("exitCode", spanDir.ExitCode),
		slog.String("spanDir", spanDir.Path),
		slog.String("spanId", spanDir.SpanID),
	)

	// 9. Stream the structured logs to the stdout.
	filep, err := spanDir.OpenStdout()
	if err != nil {
		logger.Error("spanDir.OpenStdout", slog.Any("err", err))
		env.Exit(4)
	}
	defer filep.Close()
	if _, err := io.Copy(env.Stdout, filep); err != nil {
		logger.Error("io.Copy", slog.Any("err", err))
		env.Exit(4)
	}

	// 10. Use the same exit code as the remote invocation.
	env.Exit(spanDir.ExitCode)
	return nil
}
