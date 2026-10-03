// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/bassosimone/dnscodec"
	"github.com/bassosimone/iox"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/cmd/internal/plugincommand"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
	"github.com/miekg/dns"
)

func main() {
	plugincommand.Main(0, realMain)
}

const shortDescr = "Run a measurement pipeline using the ptnop engine."

// options contains the command line options.
type options struct {
	alpn         []string
	addrPort     string
	dnsQueryName string
	dnsQueryType string
	httpBodyFile string
	httpHeaders  []string
	httpHost     string
	httpMethod   string
	httpScheme   string
	pipeline     string
	sni          string
	tags         []string
	timeout      time.Duration
	urlPath      string
}

// pipelineInput contains the validated inputs shared by all pipelines.
//
// Fields that are nil under some configurations are explicitly documented as such.
//
// Use the [newPipelineInput] function to construct.
type pipelineInput struct {
	// addrPort is the remote endpoint address to connect to.
	addrPort netip.AddrPort

	// bodyFp is where to write the HTTP response body.
	bodyFp io.WriteCloser

	// env is the testable environ.
	env *testable.Environ

	// httpReq is the HTTP request (nil except for http, https, dns-over-https).
	httpReq *http.Request

	// logger emits the structured logs.
	logger *slog.Logger

	// name is the pipeline name.
	name string

	// query is the DNS query (nil except for dns-over-*).
	query *dnscodec.Query

	// tlsConfig is the TLS config (nil except for https, tls, dns-over-https, dns-over-tls).
	tlsConfig *tls.Config
}

func realMain(ctx context.Context, args []string) error {
	// 1. Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// 2. Parse command line flags.
	opts := &options{
		timeout: 30 * time.Second,
	}
	fset := vflag.NewFlagSet("sonda-measure-ptnop", vflag.ExitOnError)

	fset.Exit = env.Exit
	fset.Stdout = env.UsageStdout
	fset.Stderr = env.Stderr

	upr := vflag.NewDefaultUsagePrinter()
	fset.UsagePrinter = upr
	upr.AddDescription(shortDescr+" Print structured logs "+
		"on the stdout. Optionally, save the HTTP response body on a separate file (only for "+
		"the `http` and `https` pipelines).",
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
		"    2 (usage error)")

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

	fset.StringSliceVar(&opts.alpn, 0, "alpn",
		"Negotiate the given `PROTO` using TLS ALPN extension.",
		"Repeat to negotiate multiple protocols.")

	fset.StringVar(&opts.addrPort, 0, "addr-port",
		"Connect to the transport endpoint at `ADDRPORT`.",
		"Quote IPv6 addresses using `[` and `]`.")

	fset.StringVar(&opts.dnsQueryName, 0, "dns-query-name",
		"Use the given DNS query `NAME`.")

	fset.StringVar(&opts.dnsQueryType, 0, "dns-query-type",
		"Use the given DNS query `TYPE` (e.g., A, AAAA).")

	fset.AutoHelp('h', "help", "Show this help message and exit.")

	fset.StringVar(&opts.httpBodyFile, 0, "http-body-file",
		"Write the HTTP response body file at `PATH`.",
		"Default: discard the response body.")

	fset.StringSliceVar(&opts.httpHeaders, 0, "http-header",
		"Send the given HTTP request `HEADER` (`KEY: VALUE`).",
		"Repeat to set additional headers.")

	fset.StringVar(&opts.httpHost, 0, "http-host",
		"Use `HOST` as the host header value.")

	fset.StringVar(&opts.httpMethod, 0, "http-method",
		"Use `METHOD` as the request method.")

	fset.StringVar(&opts.httpScheme, 0, "http-scheme",
		"Use `SCHEME` as the URL scheme.")

	fset.StringVar(&opts.pipeline, 0, "pipeline",
		"Use `PIPELINE` as the measurement pipeline.")

	fset.StringVar(&opts.sni, 0, "sni",
		"Use `HOST` in the TLS SNI extension.")

	fset.StringSliceVar(&opts.tags, 0, "tag",
		"Annotate the structured logs with the given `KEY=VALUE` tag.",
		"Repeat to annotate with the logs multiple tags.")

	fset.DurationVar(&opts.timeout, 0, "timeout",
		"Timeout the measurement after `DURATION`.",
		"Default value: `@DEFAULT_VALUE@`.")

	fset.StringVar(&opts.urlPath, 0, "url-path",
		"Use the given `PATH` as the URL path.")

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// If the user provided no arguments, print the help.
	if len(args) <= 0 {
		fset.PrintUsageString(fset.Stdout)
		env.Exit(0)
	}

	// 3. Create the structured logger.
	logger := newLogger(env, opts.tags)

	// 4. Configure the pipeline timeout.
	//
	// Note: must precede creating the HTTP request so we bound it to the deadline.
	ctx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()

	// 5. Validate the inputs.
	input, err := newPipelineInput(ctx, env, opts, logger)
	if err != nil {
		env.Exit(logUsageError(logger, "newPipelineInput", err))
	}

	// 6. Run the pipeline.
	exitCode := ptnopRunPipeline(ctx, input)

	// 7. Make sure we can close the response body.
	if err := input.Close(); err != nil {
		env.Exit(logFailure(logger, "closeFile", err, 1))
	}

	// 8. Exit with the pipeline exit code.
	env.Exit(exitCode)
	return nil
}

// newLogger creates the JSON logger bound to tags.
//
// We omit the top-level time field: library events carry their own times.
func newLogger(env *testable.Environ, tags []string) *slog.Logger {
	// Configure the JSON handler.
	logger := slog.New(slog.NewJSONHandler(env.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey && len(groups) <= 0 {
				return slog.Attr{}
			}
			return attr
		},
	}))

	// Collect unique key/values in the tags and honor `SONDA_SPAN_ID`.
	//
	// The CLI flags take precedence over the environment.
	//
	// Subsequent flags take precedence over previous flags.
	uniq := make(map[string]string)
	if value := env.Getenv("SONDA_SPAN_ID"); value != "" {
		uniq["spanId"] = value
	}
	for _, tag := range tags {
		if key, value, ok := strings.Cut(tag, "="); ok {
			uniq[key] = value
		}
	}

	// Build logger with the unique (sorted) keys.
	for _, key := range slices.Sorted(maps.Keys(uniq)) {
		logger = logger.With(key, uniq[key])
	}
	return logger
}

// logFailure logs a sondaFailure event and returns the exit code.
func logFailure(logger *slog.Logger, operation string, err error, exitCode int) int {
	logger.Error(
		"sondaFailure",
		slog.String("operation", operation),
		slog.Any("err", err),
		slog.Int("exitCode", exitCode),
	)
	return exitCode
}

// logUsageError is like [logFailure] with exit code 2.
func logUsageError(logger *slog.Logger, operation string, err error) int {
	return logFailure(logger, operation, err, 2)
}

// newPipelineInput validates the options and returns the inputs that the selected pipeline needs.
func newPipelineInput(ctx context.Context,
	env *testable.Environ, opts *options, logger *slog.Logger) (*pipelineInput, error) {
	input := &pipelineInput{
		addrPort:  netip.AddrPort{},
		bodyFp:    iox.NopWriteCloser(io.Discard),
		env:       env,
		httpReq:   nil,
		logger:    logger,
		name:      opts.pipeline,
		query:     nil,
		tlsConfig: nil,
	}

	// 1. Validate the endpoint.
	addrPort, err := netip.ParseAddrPort(opts.addrPort)
	if err != nil {
		return nil, err
	}
	input.addrPort = addrPort

	// 2. Build the TLS config, if needed.
	switch opts.pipeline {
	case "dns-over-https", "dns-over-tls", "https", "tls":
		if opts.sni == "" {
			return nil, errors.New("the SNI must not be empty")
		}
		input.tlsConfig = &tls.Config{
			NextProtos: opts.alpn,
			ServerName: opts.sni,
		}
	}

	// 3. Build the HTTP request, if needed.
	switch opts.pipeline {
	case "dns-over-https", "http", "https":
		req, err := newHTTPRequest(ctx, opts)
		if err != nil {
			return nil, err
		}
		input.httpReq = req
	}

	// 4. Build the DNS query, if needed.
	switch opts.pipeline {
	case "dns-over-https", "dns-over-tcp", "dns-over-tls", "dns-over-udp":
		qtype := dns.StringToType[opts.dnsQueryType]
		if qtype == 0 {
			return nil, fmt.Errorf("invalid dns query type: %q", opts.dnsQueryType)
		}
		query := dnscodec.NewQuery(opts.dnsQueryName, qtype)
		if _, err := query.NewMsg(); err != nil { // make sure the name is OK
			return nil, err
		}
		input.query = query
	}

	// 5. Open the body file, if needed
	//
	// Using `0640` because `/var/spool/sonda` is `_sonda:adm` and `adm` must be
	// able to read the spool without going through `sudo`.
	switch opts.pipeline {
	case "http", "https":
		if opts.httpBodyFile != "" {
			filep, err := env.OpenFile(opts.httpBodyFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0640)
			if err != nil {
				return nil, err
			}
			input.bodyFp = filep
		}
	}

	return input, nil
}

// Close implements [io.Closer]
func (pi *pipelineInput) Close() error {
	return pi.bodyFp.Close()
}

// newHTTPRequest builds the HTTP request from the options.
func newHTTPRequest(ctx context.Context, opts *options) (*http.Request, error) {
	// 1. Make sure the input is valid.
	if opts.httpHost == "" {
		return nil, errors.New("http host is empty")
	}
	if opts.httpMethod == "" {
		return nil, errors.New("http method is empty")
	}
	if opts.httpScheme == "" {
		return nil, errors.New("http scheme is empty")
	}
	if opts.urlPath == "" {
		return nil, errors.New("url path is empty")
	}

	// 2. Create the request.
	reqURL := (&url.URL{Scheme: opts.httpScheme, Host: opts.httpHost, Path: opts.urlPath}).String()
	req, err := http.NewRequestWithContext(ctx, opts.httpMethod, reqURL, http.NoBody)
	if err != nil {
		return nil, err
	}

	// 3. Assign the optional headers.
	for _, h := range opts.httpHeaders {
		key, value, ok := strings.Cut(h, ":")
		if !ok {
			return nil, fmt.Errorf("missing colon in header: %s", h)
		}
		req.Header.Add(strings.TrimSpace(key), strings.TrimSpace(value))
	}
	return req, nil
}

// errUnknownPipeline returns the error for an unknown pipeline name.
func errUnknownPipeline(name string) error {
	return fmt.Errorf("unknown pipeline name: %q", name)
}
