// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/bassosimone/deferexit"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/plugins/nob"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/sud"
	"github.com/bassosimone/vclip"
	"github.com/bassosimone/vflag"
)

func main() {
	// Transform panics into [os.Exit] calls.
	defer deferexit.Recover(os.Exit)
	env := testable.Env

	// Wrap the actual command using `vclip.RootCommand`.
	root := vclip.NewRootCommand(vclip.CommandFunc(realMain))
	root.LogFatalOnError0 = env.LogFatalOnError0

	// Execute the dispatcher command wrapper.
	root.Main(context.Background(), env.Args[1:])
}

// defaultSocketPath is the default Unix socket path user.
const defaultSocketPath = "/var/run/sonda/nob.sock"

// realMain is the actual main function of the `sonda-nobctl` plugin.
func realMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Create the `sonda-nobctl` dispatcher.
	disp := vclip.NewDispatcherCommand("sonda-nobctl", vflag.ExitOnError)
	disp.Exit = env.Exit
	disp.Stderr = env.Stderr
	disp.Stdout = env.UsageStdout

	disp.AddCommand("gc", vclip.CommandFunc(gcMain), "Garbage collect the spool directory.")
	disp.AddCommand("run", vclip.CommandFunc(runMain),
		"Run measurement pipeline and save results in the spool.")
	disp.AddCommand("show", vclip.CommandFunc(showMain),
		"Show remote file collected by a previous measurement.")

	disp.Main(ctx, args)
	return nil
}

// gcMain is the main function of the `sonda-nobctl gc` subcommand.
func gcMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Set command defaults.
	var (
		gcReq = nob.GCRequestBody{
			MaxAge: 6 * time.Hour, // --max-age <duration>
		}
		socketPath = defaultSocketPath // --socket <path>
	)

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda-nobctl gc", vflag.ExitOnError)

	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.UsageStdout

	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.DurationVar(&gcReq.MaxAge, 0, "max-age", "Remove spans older than `DURATION`.")
	fset.StringVar(&socketPath, 0, "socket",
		"Use `PATH` as the socket path instead of `@DEFAULT_VALUE@`.")

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Create the HTTP request.
	reqData := runtimex.PanicOnError1(json.Marshal(&gcReq))
	reqURL := &url.URL{
		Scheme: "http",
		Host:   "sonda.local",
		Path:   "/api/v1/gc",
	}
	req := runtimex.PanicOnError1(http.NewRequestWithContext(
		ctx, http.MethodPost, reqURL.String(), bytes.NewReader(reqData)))

	// Connect to the unit socket.
	conn, err := env.Dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		fmt.Fprintf(env.Stderr, "sonda-nobctl: %s\n", err.Error())
		env.Exit(1)
	}

	// Create the HTTP transport.
	dialer := sud.NewSingleUseDialer(conn)
	txp := &http.Transport{DialContext: dialer.DialContext}

	// Do the round trip.
	resp, err := txp.RoundTrip(req)
	if err != nil {
		fmt.Fprintf(env.Stderr, "sonda-nobctl: %s\n", err.Error())
		env.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(env.Stderr, "sonda-nobctl: %s\n", resp.Status)
		env.Exit(1)
	}

	// Read and parse the response body.
	respData, err := io.ReadAll(io.LimitReader(resp.Body, nob.MaxBodySize))
	if err != nil {
		fmt.Fprintf(env.Stderr, "sonda-nobctl: %s\n", err.Error())
		env.Exit(1)
	}
	var gcResp nob.GCResponseBody
	if err := json.Unmarshal(respData, &gcResp); err != nil {
		fmt.Fprintf(env.Stderr, "sonda-nobctl: %s\n", err.Error())
		env.Exit(1)
	}

	return nil
}

// runMain is the main function of the `sonda-nobctl run` subcommand.
func runMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Set command defaults.
	//
	// Note: the server owns the defaults and we just pass
	// whatever flag the user has provided.
	var (
		runReq = nob.RunRequestBody{
			ALPN:        []string{},       // --alpn <proto> ...
			AddrPort:    "",               // --addr-port <addr:port>
			HTTPHeaders: []string{},       // --http-header "key: value" ...
			HTTPHost:    "",               // --http-host <host>
			HTTPMethod:  "",               // --http-method <method>
			HTTPScheme:  "",               // --http-scheme <scheme>
			Pipeline:    "s",              // --pipeline <name>
			Protocol:    "",               // --protocol <proto>
			SNI:         "",               // --sni <host>
			Tags:        []string{},       // --tag <tag> ...
			Timeout:     30 * time.Second, // --timeout <duration>
			URLPath:     "",               // --url-path <path>
		}
		socketPath = defaultSocketPath // --socket <path>
	)

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda-nobctl run", vflag.ExitOnError)

	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.UsageStdout

	fset.StringSliceVar(&runReq.ALPN, 0, "alpn",
		"Negotiate the given `PROTO` using ALPN.",
		"Repeat to negotiate multiple ALPN values.",
		"Example: `--alpn h2 --alpn http/1.1`.")

	fset.StringVar(&runReq.AddrPort, 0, "addr-port",
		"Connect to the transport endpoint at `ADDRPORT`.",
		"Example: `8.8.8.8:443`, `[::1]:443`.")

	fset.AutoHelp('h', "help", "Show this help message and exit.")

	fset.StringSliceVar(&runReq.HTTPHeaders, 0, "http-header",
		"Send the given HTTP request `HEADER`.",
		"Repeat to set additional headers.",
		"Example: `--header 'A: a' --header `B: b`.")

	fset.StringVar(&runReq.HTTPHost, 0, "http-host",
		"Use `HOST` as the host header value.")

	fset.StringVar(&runReq.HTTPMethod, 0, "http-method",
		"Use `METHOD` as the request method.")

	fset.StringVar(&runReq.HTTPScheme, 0, "http-scheme",
		"Use `SCHEME` as the URL scheme / H2 pseudo-header.")

	fset.StringVar(&runReq.Pipeline, 0, "pipeline",
		"Use `PIPELINE` as the measurement pipeline.",
		"One of: http, https, tcp, tls.")

	fset.StringVar(&runReq.Protocol, 0, "protocol",
		"Use `PROTO` as the transport protocol.")

	fset.StringVar(&socketPath, 0, "socket",
		"Use `PATH` as the sonda-nob Unix socket path.",
		"Default value: `@DEFAULT_VALUE@`.")

	fset.StringVar(&runReq.SNI, 0, "sni",
		"Send `HOST` in the server-name indication extension.")

	fset.StringSliceVar(&runReq.Tags, 0, "tag",
		"Annotate the structured logs with the given `TAG`.",
		"Repeat to annotate with multiple tags.",
		"Example: `--tag a --tag b --tag c`.")

	fset.DurationVar(&runReq.Timeout, 0, "timeout",
		"Timeout the measurement after `DURATION` has elapsed.",
		"Default value: `@DEFAULT_VALUE@`.")

	fset.StringVar(&runReq.URLPath, 0, "url-path",
		"Use the given `PATH` as the URL path.")

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Create the HTTP request.
	reqData := runtimex.PanicOnError1(json.Marshal(&runReq))
	reqURL := &url.URL{
		Scheme: "http",
		Host:   "sonda.local",
		Path:   "/api/v1/run",
	}
	req := runtimex.PanicOnError1(http.NewRequestWithContext(
		ctx, http.MethodPost, reqURL.String(), bytes.NewReader(reqData)))

	// Connect to the unit socket.
	conn, err := env.Dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		fmt.Fprintf(env.Stderr, "sonda-nobctl: %s\n", err.Error())
		env.Exit(1)
	}

	// Create the HTTP transport.
	dialer := sud.NewSingleUseDialer(conn)
	txp := &http.Transport{DialContext: dialer.DialContext}

	// Do the round trip.
	resp, err := txp.RoundTrip(req)
	if err != nil {
		fmt.Fprintf(env.Stderr, "sonda-nobctl: %s\n", err.Error())
		env.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(env.Stderr, "sonda-nobctl: %s\n", resp.Status)
		env.Exit(1)
	}

	// Read and parse the response body.
	respData, err := io.ReadAll(io.LimitReader(resp.Body, nob.MaxBodySize))
	if err != nil {
		fmt.Fprintf(env.Stderr, "sonda-nobctl: %s\n", err.Error())
		env.Exit(1)
	}
	var runResp nob.RunResponseBody
	if err := json.Unmarshal(respData, &runResp); err != nil {
		fmt.Fprintf(env.Stderr, "sonda-nobctl: %s\n", err.Error())
		env.Exit(1)
	}

	// Print the measurement span ID.
	fmt.Fprintf(env.Stdout, "%s\n", runResp.SpanID)
	return nil
}

// showMain is the main function of the `sonda-nobctl show` subcommand.
func showMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Set command defaults.
	var (
		fileName   = "stdout.json"     // --file-name <string>
		spanID     string              // --span-id <string>
		socketPath = defaultSocketPath // --socket <path>
	)

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda-nobctl show", vflag.ExitOnError)

	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.UsageStdout

	fset.StringVar(&fileName, 0, "file-name",
		"`NAME` of the remote measurement file to fetch.",
		"Default value: `@DEFAULT_VALUE@`.",
		"One of: body.bin, exitcode.txt, request.json, stderr.txt, stdout.txt")

	fset.AutoHelp('h', "help", "Show this help message and exit.")

	fset.StringVar(&spanID, 0, "span-id",
		"Unique `SPAN_ID` that identifies the measurement.",
		"This flag is required.",
		"Use the value returned by `sonda-nobctl run`.")

	fset.StringVar(&socketPath, 0, "socket",
		"Use `PATH` as the sonda-nob Unix socket path.",
		"Default value: `@DEFAULT_VALUE@`.")

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	if spanID == "" {
		fmt.Fprintf(env.Stderr, "sonda-nobctl: the `--span-id` flag is required.\n")
		env.Exit(2)
	}

	// Create the HTTP request.
	reqURL := &url.URL{
		Scheme: "http",
		Host:   "sonda.local",
		Path:   fmt.Sprintf("/api/v1/spans/%s/%s", spanID, fileName),
	}
	req := runtimex.PanicOnError1(http.NewRequestWithContext(
		ctx, http.MethodGet, reqURL.String(), http.NoBody))

	// Connect to the unit socket.
	conn, err := env.Dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		fmt.Fprintf(env.Stderr, "sonda-nobctl: %s\n", err.Error())
		env.Exit(1)
	}

	// Create the HTTP transport.
	dialer := sud.NewSingleUseDialer(conn)
	txp := &http.Transport{DialContext: dialer.DialContext}

	// Do the round trip.
	resp, err := txp.RoundTrip(req)
	if err != nil {
		fmt.Fprintf(env.Stderr, "sonda-nobctl: %s\n", err.Error())
		env.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(env.Stderr, "sonda-nobctl: %s\n", resp.Status)
		env.Exit(1)
	}

	// Read and copy the response body.
	if _, err := io.Copy(env.Stdout, resp.Body); err != nil {
		fmt.Fprintf(env.Stderr, "sonda-nobctl: %s\n", err.Error())
		env.Exit(1)
	}
	return nil
}
