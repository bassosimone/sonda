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
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/bassosimone/closepool"
	"github.com/bassosimone/dnscodec"
	"github.com/bassosimone/ptnop"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/plugins/nob"
	"github.com/google/uuid"
	"github.com/miekg/dns"
)

// Forward declarations from the importable [nob] package.
type (
	runRequestBody  = nob.RunRequestBody
	runResponseBody = nob.RunResponseBody
)

// Run handles `POST /api/v1/run`.
func (h *handler) Run(w http.ResponseWriter, r *http.Request) {
	// 1. Parse request body.
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxRequestBodySize))
	decoder.DisallowUnknownFields()
	var reqb runRequestBody
	if err := decoder.Decode(&reqb); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// 2. Run.
	spanID, err := h.runMain(r.Context(), &reqb)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// 3. Send response.
	rrb := runResponseBody{SpanID: spanID}
	w.Write(append(runtimex.PanicOnError1(json.Marshal(rrb)), '\n'))
}

// runMain runs a specific measurement and returns its span ID.
func (h *handler) runMain(ctx context.Context, reqb *runRequestBody) (string, error) {
	// Mint a new span ID.
	var spanID = uuid.Must(uuid.NewV7()).String()

	// Build the spool directory path.
	spanDir := pathsSpanDir(h.dir, spanID)
	tmpDir := pathsSpanDirTmp(h.dir, spanID)

	// Create the temporary spool directory.
	if err := h.env.MkdirAll(tmpDir, 0750); err != nil {
		h.logger.Warn("mkdir", slog.Any("err", err))
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	// Record the received configuration.
	argvData := append(runtimex.PanicOnError1(json.Marshal(reqb)), '\n')
	if err := h.env.WriteFile(pathsSpanRequestJSON(tmpDir), argvData, 0600); err != nil {
		h.logger.Warn("writeFile", slog.Any("err", err))
		return "", err
	}

	// Open stdout, stderr, body files in the spool directory.
	closers := &closepool.Pool{}
	defer closers.Close() // idempotent

	openFlags := os.O_CREATE | os.O_TRUNC | os.O_WRONLY
	stdoutFile, err := h.env.OpenFile(pathsSpanStdoutJSON(tmpDir), openFlags, 0600)
	if err != nil {
		h.logger.Warn("openFile", slog.Any("err", err))
		return "", err
	}
	closers.Add(stdoutFile)

	stderrFile, err := h.env.OpenFile(pathsSpanStderrTxt(tmpDir), openFlags, 0600)
	if err != nil {
		h.logger.Warn("openFile", slog.Any("err", err))
		return "", err
	}
	closers.Add(stderrFile)

	bodyFile, err := h.env.OpenFile(spanBodyBin(tmpDir), openFlags, 0600)
	if err != nil {
		h.logger.Warn("openFile", slog.Any("err", err))
		return "", err
	}
	closers.Add(bodyFile)

	// Run the command and record the exit code.
	rla := &runLocalArgs{
		body:   bodyFile,
		spanID: spanID,
		stdout: stdoutFile,
		stderr: stderrFile,
	}
	exitCode := h.runPipeline(ctx, rla, reqb)

	// Make sure we can successfully close all opened files.
	if err := closers.Close(); err != nil {
		h.logger.Warn("close", slog.Any("err", err))
		return "", err
	}

	// Write the exit code file to the spool directory.
	exitCodeData := []byte(strconv.Itoa(exitCode) + "\n")
	if err := h.env.WriteFile(pathsSpanExitCodeTxt(tmpDir), exitCodeData, 0600); err != nil {
		h.logger.Warn("writeFile", slog.Any("err", err))
		return "", err
	}

	// Atomically rename the temporary directory to the final path.
	if err := h.env.Rename(tmpDir, spanDir); err != nil {
		h.logger.Warn("mv", slog.Any("err", err))
		return "", err
	}
	return spanID, nil
}

// runLocalArgs contains the local args passed to `runMeasure`.
type runLocalArgs struct {
	// body is the body file.
	body io.Writer

	// spanID is the minted span ID.
	spanID string

	// stdout is the stdout file.
	stdout io.Writer

	// stderr is the stderr file.
	stderr io.Writer
}

func runNewSlogLogger(stdout io.Writer, spanID string, tags []string) *slog.Logger {
	// 1. Create JSON logger omitting the `time` field.
	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey && len(groups) <= 0 {
				return slog.Attr{}
			}
			return attr
		},
	}))

	// 2. Bind the logger to the tags.
	for _, tag := range tags {
		if key, value, ok := strings.Cut(tag, "="); ok {
			logger = logger.With(key, value)
		}
	}

	// 3. Bind the logger to the minted span ID.
	return logger.With("spanID", spanID)
}

func runNewTLSConfig(reqb *runRequestBody) (*tls.Config, error) {
	if reqb.SNI == "" {
		return nil, errors.New("the SNI field must not be empty")
	}
	tlsConfig := &tls.Config{
		ServerName: reqb.SNI,
		NextProtos: reqb.ALPN,
	}
	return tlsConfig, nil
}

func runNewHTTPRequest(ctx context.Context, reqb *runRequestBody) (*http.Request, error) {
	// 1. Make sure the input is valid.
	if reqb.HTTPScheme == "" {
		return nil, errors.New("http scheme is empty")
	}
	if reqb.HTTPHost == "" {
		return nil, errors.New("http host is empty")
	}
	if reqb.URLPath == "" {
		return nil, errors.New("url path is empty")
	}

	// 2. Synthesize the URL.
	httpURL := (&url.URL{
		Scheme: reqb.HTTPScheme,
		Host:   reqb.HTTPHost,
		Path:   reqb.URLPath,
	}).String()

	// 3. Create the request.
	httpReq, err := http.NewRequestWithContext(ctx, reqb.HTTPMethod, httpURL, http.NoBody)
	if err != nil {
		return nil, err
	}

	// 4. Assign optional headers.
	for _, h := range reqb.HTTPHeaders {
		key, value, ok := strings.Cut(h, ":")
		if !ok {
			return nil, fmt.Errorf("missing colon in header: %s", h)
		}
		httpReq.Header.Add(strings.TrimSpace(key), strings.TrimSpace(value))
	}
	return httpReq, nil
}

// runMaybe potentially contains a value.
type runMaybe[T any] struct {
	ok bool
	v  T
}

// Set sets the wrapped value.
func (rm *runMaybe[T]) Set(v T) {
	rm.ok = true
	rm.v = v
}

// Unwrap returns the wrapped value or panics if not set.
func (rm *runMaybe[T]) Unwrap() T {
	runtimex.Assert(rm.ok)
	return rm.v
}

func runPipelineHTTP(
	ctx context.Context,
	addrPort netip.AddrPort,
	bodyFp io.Writer,
	dialer ptnop.Func[netip.AddrPort, *ptnop.HTTPConn],
	logger *slog.Logger,
	req *http.Request,
) int {
	// 1. Dial the HTTP connection.
	httpConn := dialer.Call(ctx, addrPort)
	defer httpConn.Close()

	// 2. Perform the HTTP round trip.
	resp, err := httpConn.RoundTrip(req)
	if err != nil {
		logger.Error(
			"sondaFailure",
			slog.String("operation", "roundTrip"),
			slog.Any("err", err),
			slog.Int("exitCode", 1),
		)
		return 1
	}
	defer resp.Body.Close()

	// 3. Drain the response body.
	if _, err := io.Copy(bodyFp, resp.Body); err != nil {
		logger.Error(
			"sondaFailure",
			slog.String("operation", "readBody"),
			slog.Any("err", err),
			slog.Int("exitCode", 1),
		)
		return 1
	}
	return 0
}

func runPipelineDNS(
	ctx context.Context,
	addrPort netip.AddrPort,
	dialer ptnop.Func[netip.AddrPort, ptnop.DNSConn],
	logger *slog.Logger,
	query *dnscodec.Query,
) int {
	// 1. Dial the DNS connection.
	dnsConn := dialer.Call(ctx, addrPort)
	defer dnsConn.Close()

	// 2. Perform the DNS exchange.
	resp, err := dnsConn.Exchange(ctx, query)
	if err != nil {
		logger.Error(
			"sondaFailure",
			slog.String("operation", "exchange"),
			slog.Any("err", err),
			slog.Int("exitCode", 1),
		)
		return 1
	}

	// TODO(bassosimone): use the response
	_ = resp
	return 0
}

// runPipeline runs a measurement pipeline.
func (h *handler) runPipeline(ctx context.Context, rla *runLocalArgs, reqb *runRequestBody) int {
	// 1. Create the structured logger for emitting the JSON events.
	logger := runNewSlogLogger(rla.stdout, rla.spanID, reqb.Tags)

	// 2. Configure the pipeline timeout.
	//
	// Note: must preceed creating an HTTP request so we bound it to the deadline.
	ctx, cancel := context.WithTimeout(ctx, reqb.Timeout)
	defer cancel()

	// 3. Make sure the target endpoint is valid.
	addrPort, err := netip.ParseAddrPort(reqb.AddrPort)
	if err != nil {
		logger.Error(
			"sondaFailure",
			slog.String("operation", "parseAddrPort"),
			slog.Any("err", err),
			slog.Int("exitCode", 2),
		)
		return 2
	}

	// 4. Make sure the TLS configuration is valid.
	tlsConfig := &runMaybe[*tls.Config]{}
	switch reqb.Pipeline {
	case "dns-over-https", "dns-over-tls", "https", "tls":
		tconf, err := runNewTLSConfig(reqb)
		if err != nil {
			logger.Error(
				"sondaFailure",
				slog.String("operation", "newTlsConfig"),
				slog.Any("err", err),
				slog.Int("exitCode", 2),
			)
			return 2
		}
		tlsConfig.Set(tconf)

	default:
		// nothing
	}

	// 5. Make sure the HTTP configuration is valid.
	httpReq := &runMaybe[*http.Request]{}
	switch reqb.Pipeline {
	case "dns-over-https", "http", "https":
		hreq, err := runNewHTTPRequest(ctx, reqb)
		if err != nil {
			logger.Error(
				"sondaFailure",
				slog.String("operation", "newHttpRequest"),
				slog.Any("err", err),
				slog.Int("exitCode", 2),
			)
			return 2
		}
		httpReq.Set(hreq)

	default:
		// nothing
	}

	// 6. Make sure the DNS configuration is valid.
	query := &runMaybe[*dnscodec.Query]{}
	switch reqb.Pipeline {
	case "dns-over-udp", "dns-over-tcp", "dns-over-tls", "dns-over-https":
		qtype := dns.StringToType[reqb.DNSQueryType]
		if qtype == 0 {
			logger.Error(
				"sondaFailure",
				slog.String("operation", "newDnsRequest"),
				slog.String("err", fmt.Sprintf("invalid dns query type: %s", reqb.DNSQueryType)),
				slog.Int("exitCode", 2),
			)
			return 2
		}
		dq := dnscodec.NewQuery(reqb.DNSQueryName, qtype)
		if _, err := dq.NewMsg(); err != nil {
			logger.Error(
				"sondaFailure",
				slog.String("operation", "newDnsRequest"),
				slog.Any("err", err),
				slog.Int("exitCode", 2),
			)
			return 2
		}
		query.Set(dq)

	default:
		// nothing
	}

	// 7. Create the shared pipeline configuration.
	cfg := ptnop.NewConfig()
	cfg.SLogger = logger
	cfg.Dialer = h.env.Dialer

	// 8. Do something different depending on the pipeline.
	switch reqb.Pipeline {
	case "dns-over-https":
		return runPipelineDNS(
			ctx,
			addrPort,
			ptnop.Compose6(
				ptnop.NewConnectFunc(cfg, "tcp"),
				ptnop.NewObserveConnFunc(cfg),
				ptnop.NewCancelWatchFunc(),
				ptnop.NewTLSHandshakeFunc(cfg, tlsConfig.Unwrap()),
				ptnop.NewHTTPConnFunc(cfg),
				ptnop.NewDNSOverHTTPSConnFunc(cfg, httpReq.Unwrap().URL.String()),
			),
			logger,
			query.Unwrap(),
		)

	case "dns-over-tcp":
		return runPipelineDNS(
			ctx,
			addrPort,
			ptnop.Compose4(
				ptnop.NewConnectFunc(cfg, "tcp"),
				ptnop.NewObserveConnFunc(cfg),
				ptnop.NewCancelWatchFunc(),
				ptnop.NewDNSOverTCPConnFunc(cfg),
			),
			logger,
			query.Unwrap(),
		)

	case "dns-over-tls":
		return runPipelineDNS(
			ctx,
			addrPort,
			ptnop.Compose5(
				ptnop.NewConnectFunc(cfg, "tcp"),
				ptnop.NewObserveConnFunc(cfg),
				ptnop.NewCancelWatchFunc(),
				ptnop.NewTLSHandshakeFunc(cfg, tlsConfig.Unwrap()),
				ptnop.NewDNSOverTLSConnFunc(cfg),
			),
			logger,
			query.Unwrap(),
		)

	case "dns-over-udp":
		return runPipelineDNS(
			ctx,
			addrPort,
			ptnop.Compose4(
				ptnop.NewConnectFunc(cfg, "udp"),
				ptnop.NewObserveConnFunc(cfg),
				ptnop.NewCancelWatchFunc(),
				ptnop.NewDNSOverUDPConnFunc(cfg),
			),
			logger,
			query.Unwrap(),
		)

	case "http":
		return runPipelineHTTP(
			ctx,
			addrPort,
			rla.body,
			ptnop.Compose4(
				ptnop.NewConnectFunc(cfg, "tcp"),
				ptnop.NewObserveConnFunc(cfg),
				ptnop.NewCancelWatchFunc(),
				ptnop.NewHTTPConnFunc(cfg),
			),
			logger,
			httpReq.Unwrap(),
		)

	case "https":
		return runPipelineHTTP(
			ctx,
			addrPort,
			rla.body,
			ptnop.Compose5(
				ptnop.NewConnectFunc(cfg, "tcp"),
				ptnop.NewObserveConnFunc(cfg),
				ptnop.NewCancelWatchFunc(),
				ptnop.NewTLSHandshakeFunc(cfg, tlsConfig.Unwrap()),
				ptnop.NewHTTPConnFunc(cfg),
			),
			logger,
			httpReq.Unwrap(),
		)

	case "tls":
		pipeline := ptnop.Compose4(
			ptnop.NewConnectFunc(cfg, "tcp"),
			ptnop.NewObserveConnFunc(cfg),
			ptnop.NewCancelWatchFunc(),
			ptnop.NewTLSHandshakeFunc(cfg, tlsConfig.Unwrap()),
		)
		result := pipeline.Call(ctx, addrPort)
		if result.Err != nil {
			logger.Error(
				"sondaFailure",
				slog.String("operation", "tlsPipeline"),
				slog.Any("err", err),
				slog.Int("exitCode", 1),
			)
			return 1
		}
		result.V.Close()
		return 0

	case "tcp":
		pipeline := ptnop.Compose3(
			ptnop.NewConnectFunc(cfg, "tcp"),
			ptnop.NewObserveConnFunc(cfg),
			ptnop.NewCancelWatchFunc(),
		)
		result := pipeline.Call(ctx, addrPort)
		if result.Err != nil {
			logger.Error(
				"sondaFailure",
				slog.String("operation", "tlsPipeline"),
				slog.Any("err", err),
				slog.Int("exitCode", 1),
			)
			return 1
		}
		result.V.Close()
		return 0

	default:
		logger.Error(
			"sondaFailure",
			slog.String("operation", "selectPipeline"),
			slog.String("err", fmt.Sprintf("unknown pipeline name: %q", reqb.Pipeline)),
			slog.Int("exitCode", 2),
		)
		return 2
	}
}
