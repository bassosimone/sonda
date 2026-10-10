// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
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
	"strconv"
	"strings"
	"time"

	"github.com/bassosimone/closepool"
	"github.com/bassosimone/dnscodec"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/ptnoppaths"
	"github.com/bassosimone/sonda/internal/ptnoprpc"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/google/uuid"
	"github.com/miekg/dns"
)

const (
	// defaultTimeout is the pipeline timeout used when the request does not
	// specify one. Same as the `sonda measure-ptnop` default.
	defaultTimeout = 30 * time.Second

	// maxTimeout is the maximum pipeline timeout a request may ask for, so
	// that a single request cannot hold the connection for too long. Same
	// as the `sonda spool run` default.
	maxTimeout = 5 * time.Minute
)

// newSpanID returns a new span ID, which is a UUIDv7 in canonical form.
func newSpanID() string {
	return uuid.Must(uuid.NewV7()).String()
}

// serveLine serves a raw request line received on the standard input and
// returns the response to send back. It never returns nil.
//
// The logger is the operational logger (i.e., the journal). The measurement
// events go to the span's `stdout.txt` file instead.
//
// The peer contains the client credentials, possibly [unknownPeerCreds].
func serveLine(
	ctx context.Context,
	env *testable.Environ,
	logger *slog.Logger,
	rawLine []byte,
	spoolDir string,
	peer peerCreds,
) *ptnoprpc.Response {
	// 1. Parse the raw line received on the stdin.
	var req ptnoprpc.Request
	if err := json.Unmarshal(rawLine, &req); err != nil {
		return &ptnoprpc.Response{Error: err.Error()}
	}
	resp := &ptnoprpc.Response{ID: req.ID}

	// 2. Configure the pipeline timeout.
	//
	// Note: must precede creating the HTTP request so we bound it to the deadline.
	if req.Timeout <= 0 {
		req.Timeout = defaultTimeout
	}
	if req.Timeout > maxTimeout {
		resp.Error = fmt.Sprintf(
			"%stimeout %s exceeds the maximum %s",
			ptnoprpc.UsageErrorPrefix,
			req.Timeout,
			maxTimeout,
		)
		return resp
	}
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	// 3. Validate the inputs before touching the spool, such that an invalid
	// request does not leave a span directory behind.
	input, err := newPipelineInput(ctx, env, &req)
	if err != nil {
		resp.Error = fmt.Sprintf("%s%s", ptnoprpc.UsageErrorPrefix, err.Error())
		return resp
	}

	// 4. Build the proper destination directory.
	spanID := newSpanID()
	spanDir := ptnoppaths.SpanDir(spoolDir, spanID)
	tmpDir := ptnoppaths.SpanDirTmp(spoolDir, spanID)
	resp.SpanID = spanID

	// From now on, failures are failures of the server rather than of the
	// request, hence we also log them to the journal.
	serverFailure := func(operation string, err error) *ptnoprpc.Response {
		logger.Error(operation, slog.String("spanId", spanID), slog.Any("err", err))
		resp.Error = err.Error()
		return resp
	}

	// 5. Create the temporary spool directory.
	//
	// Note that `/var/spool/sonda` is `02750 _sonda:_sonda` so we use `0750` when creating
	// directories and `0640` when creating files to allow `_sonda` to read.
	if err := env.MkdirAll(tmpDir, 0750); err != nil {
		return serverFailure("env.MkdirAll", err)
	}

	// 6. Record the request that will be executed and, if known, who sent it.
	//
	// We keep these separate because the request is what the client sent while
	// the peer is what we observed.
	reqData := runtimex.PanicOnError1(json.Marshal(req)) // always serializable
	reqData = append(reqData, '\n')
	if err := env.WriteFile(ptnoppaths.SpanRequestJSON(tmpDir), reqData, 0640); err != nil {
		return serverFailure("env.WriteFile", err)
	}
	peerData := runtimex.PanicOnError1(json.Marshal(peer)) // always serializable
	peerData = append(peerData, '\n')
	if err := env.WriteFile(ptnoppaths.SpanPeerJSON(tmpDir), peerData, 0640); err != nil {
		return serverFailure("env.WriteFile", err)
	}

	// 7. Open stdout in the spool directory.
	closers := &closepool.Pool{}
	defer closers.Close() // idempotent

	openFlags := os.O_CREATE | os.O_TRUNC | os.O_WRONLY
	stdoutFile, err := env.OpenFile(ptnoppaths.SpanStdout(tmpDir), openFlags, 0640)
	if err != nil {
		return serverFailure("env.OpenFile", err)
	}
	closers.Add(stdoutFile)

	// 8. Open the body file, if needed.
	switch req.Pipeline {
	case "http", "https":
		if req.HTTPBodyFile {
			bodyFile, err := env.OpenFile(ptnoppaths.SpanBodyBin(tmpDir), openFlags, 0640)
			if err != nil {
				return serverFailure("env.OpenFile", err)
			}
			closers.Add(bodyFile)
			input.bodyFp = bodyFile
		}
	}

	// 9. Create the structured logger writing the measurement events.
	input.logger = newLogger(stdoutFile, spanID, req.Tags, req.Optimize)

	// 10. Run the pipeline.
	exitCode := ptnopRunPipeline(ctx, input)

	// 11. Make sure we successfully closed all the opened resources.
	if err := closers.Close(); err != nil {
		return serverFailure("closers.Close", err)
	}

	// 12. Write the exit code to the spool directory.
	exitCodeData := []byte(strconv.Itoa(exitCode) + "\n")
	if err := env.WriteFile(ptnoppaths.SpanExitCode(tmpDir), exitCodeData, 0640); err != nil {
		return serverFailure("env.WriteFile", err)
	}

	// 13. Atomically rename the temporary directory to the final path.
	if err := env.Rename(tmpDir, spanDir); err != nil {
		return serverFailure("env.Rename", err)
	}

	// 14. Tell the caller that we succeeded.
	resp.SpanDir = spanDir
	resp.ExitCode = &exitCode
	return resp
}

// newLogger creates the JSON logger writing to w and bound to tags. When optimize
// is "size", we trim the emitted events as documented by [*ptnoprpc.Request].
//
// We omit the top-level time field: library events carry their own times.
func newLogger(w io.Writer, spanID string, tags []string, optimize string) *slog.Logger {
	// Configure the JSON handler.
	level := slog.LevelDebug
	if optimize == "size" {
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey && len(groups) <= 0 {
				return slog.Attr{}
			}
			return attr
		},
	}))

	// Collect unique key/values in the tags.
	//
	// The spanID we minted always takes precedence.
	uniq := make(map[string]string)
	for _, tag := range tags {
		if key, value, ok := strings.Cut(tag, "="); ok {
			uniq[key] = value
		}
	}
	uniq["spanId"] = spanID

	// Build logger with the unique (sorted) keys.
	for _, key := range slices.Sorted(maps.Keys(uniq)) {
		logger = logger.With(key, uniq[key])
	}
	return logger
}

// pipelineInput contains the validated inputs shared by all pipelines.
//
// Fields that are nil under some configurations are explicitly documented as such.
//
// Use the [newPipelineInput] function to construct.
type pipelineInput struct {
	// addrPort is the remote endpoint address to connect to.
	addrPort netip.AddrPort

	// bodyFp is where to write the HTTP response body (io.Discard unless
	// [serveLine] replaces it with the span's body file).
	bodyFp io.Writer

	// env is the testable environ.
	env *testable.Environ

	// httpReq is the HTTP request (nil except for http, https, dns-over-https).
	httpReq *http.Request

	// logger emits the structured logs (nil until [serveLine] creates the span dir).
	logger *slog.Logger

	// name is the pipeline name.
	name string

	// query is the DNS query (nil except for dns-over-*).
	query *dnscodec.Query

	// tlsConfig is the TLS config (nil except for https, tls, dns-over-https, dns-over-tls).
	tlsConfig *tls.Config
}

// newPipelineInput validates the request and returns the inputs that the selected
// pipeline needs. This function does not perform any I/O: in particular, it does not create
// files, such that [serveLine] can reject a request before touching the spool.
func newPipelineInput(
	ctx context.Context, env *testable.Environ, req *ptnoprpc.Request) (*pipelineInput, error) {
	// 0. Initialize with defaults
	input := &pipelineInput{
		addrPort:  netip.AddrPort{},
		bodyFp:    io.Discard, // we are always able to "write"
		env:       env,
		httpReq:   nil,
		logger:    nil,
		name:      req.Pipeline,
		query:     nil,
		tlsConfig: nil,
	}

	// 1. Validate the pipeline name.
	switch req.Pipeline {
	case "dns-over-https", "dns-over-tcp", "dns-over-tls", "dns-over-udp",
		"http", "https", "stun", "tcp", "tls":
	default:
		return nil, errUnknownPipeline(req.Pipeline)
	}

	// 2. Validate the endpoint.
	addrPort, err := netip.ParseAddrPort(req.AddrPort)
	if err != nil {
		return nil, err
	}
	input.addrPort = addrPort

	// 3. Build the TLS config, if needed.
	switch req.Pipeline {
	case "dns-over-https", "dns-over-tls", "https", "tls":
		if req.SNI == "" {
			return nil, errors.New("the SNI must not be empty")
		}
		input.tlsConfig = &tls.Config{
			NextProtos: req.ALPN,
			ServerName: req.SNI,
		}
	}

	// 4. Build the HTTP request, if needed.
	switch req.Pipeline {
	case "dns-over-https", "http", "https":
		httpReq, err := newHTTPRequest(ctx, req)
		if err != nil {
			return nil, err
		}
		input.httpReq = httpReq
	}

	// 5. Build the DNS query, if needed.
	switch req.Pipeline {
	case "dns-over-https", "dns-over-tcp", "dns-over-tls", "dns-over-udp":
		qtype := dns.StringToType[req.DNSQueryType]
		if qtype == 0 {
			return nil, fmt.Errorf("invalid dns query type: %q", req.DNSQueryType)
		}
		query := dnscodec.NewQuery(req.DNSQueryName, qtype)
		if _, err := query.NewMsg(); err != nil { // make sure the name is OK
			return nil, err
		}
		input.query = query
	}

	return input, nil
}

// newHTTPRequest builds the HTTP request from the request options.
func newHTTPRequest(ctx context.Context, req *ptnoprpc.Request) (*http.Request, error) {
	// 1. Make sure the input is valid.
	if req.HTTPHost == "" {
		return nil, errors.New("http host is empty")
	}
	if req.HTTPMethod == "" {
		return nil, errors.New("http method is empty")
	}
	if req.HTTPScheme == "" {
		return nil, errors.New("http scheme is empty")
	}
	if req.URLPath == "" {
		return nil, errors.New("url path is empty")
	}

	// 2. Create the request.
	reqURL := (&url.URL{Scheme: req.HTTPScheme, Host: req.HTTPHost, Path: req.URLPath}).String()
	httpReq, err := http.NewRequestWithContext(ctx, req.HTTPMethod, reqURL, http.NoBody)
	if err != nil {
		return nil, err
	}

	// 3. Assign the optional headers.
	for _, h := range req.HTTPHeaders {
		key, value, ok := strings.Cut(h, ":")
		if !ok {
			return nil, fmt.Errorf("missing colon in header: %s", h)
		}
		httpReq.Header.Add(strings.TrimSpace(key), strings.TrimSpace(value))
	}
	return httpReq, nil
}

// errUnknownPipeline returns the error for an unknown pipeline name.
func errUnknownPipeline(name string) error {
	return fmt.Errorf("unknown pipeline name: %q", name)
}
