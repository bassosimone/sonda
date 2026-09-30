// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bassosimone/closepool"
	"github.com/bassosimone/errclass"
	"github.com/bassosimone/nop"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/plugins/nob"
)

// Forward declarations from the importable [nob] package.
type (
	runRequestBody  = nob.RunRequestBody
	runResponseBody = nob.RunResponseBody
)

// Run handles `POST /api/v1/run`.
func (h *handler) Run(w http.ResponseWriter, r *http.Request) {
	// 1. Assign reasonable defaults.
	reqb := runRequestBody{
		ALPN:        []string{},
		AddrPort:    "8.8.8.8:443",
		HTTPHeaders: []string{},
		HTTPHost:    "dns.google",
		HTTPMethod:  "GET",
		HTTPScheme:  "https",
		Pipeline:    "https",
		Protocol:    "tcp",
		SNI:         "dns.google",
		Tags:        []string{},
		Timeout:     30 * time.Second,
		URLPath:     "/",
	}

	// 2. Parse request body.
	//
	// TODO(bassosimone): use `DisallowUnknownFields` here.
	rawReqb, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodySize))
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(rawReqb, &reqb); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// 3. Run.
	spanID, err := h.runMain(r.Context(), &reqb)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// 4. Send response.
	rrb := runResponseBody{SpanID: spanID}
	w.Write(append(runtimex.PanicOnError1(json.Marshal(rrb)), '\n'))
}

// runMain runs a specific measurement and returns its span ID.
func (h *handler) runMain(ctx context.Context, reqb *runRequestBody) (string, error) {
	// Mint a new span ID.
	var spanID = nop.NewSpanID()

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
	exitCode := h.runMeasure(ctx, rla, reqb)

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

// runMeasure runs a measurement.
func (h *handler) runMeasure(ctx context.Context, rla *runLocalArgs, reqb *runRequestBody) int {
	// Emit structured logs to the stdout tied together by the span ID.
	logger := slog.New(slog.NewJSONHandler(rla.stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	logger = logger.With("spanID", rla.spanID)
	for _, tag := range reqb.Tags {
		if key, value, ok := strings.Cut(tag, "="); ok {
			logger = logger.With(key, value)
		}
	}

	// Parse the target addrPort.
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

	// Create TLS configuration.
	tlsConfig := &tls.Config{ServerName: reqb.SNI, NextProtos: reqb.ALPN}

	// Build the HTTP request.
	httpURL := (&url.URL{
		Scheme: reqb.HTTPScheme,
		Host:   reqb.HTTPHost,
		Path:   reqb.URLPath,
	}).String()
	httpReq, err := http.NewRequestWithContext(ctx, reqb.HTTPMethod, httpURL, http.NoBody)
	if err != nil {
		logger.Error(
			"sondaFailure",
			slog.String("operation", "newHttpRequest"),
			slog.Any("err", err),
			slog.Int("exitCode", 2),
		)
		return 2
	}
	for _, h := range reqb.HTTPHeaders {
		key, value, ok := strings.Cut(h, ":")
		if !ok {
			logger.Error(
				"sondaFailure",
				slog.String("operation", "parseHeaders"),
				slog.String("header", h),
				slog.String("err", "missing colon"),
				slog.Int("exitCode", 2),
			)
			return 2
		}
		httpReq.Header.Add(strings.TrimSpace(key), strings.TrimSpace(value))
	}

	// Create the shared pipeline configuration.
	cfg := nop.NewConfig()
	cfg.Dialer = h.env.Dialer
	cfg.ErrClassifier = nop.ErrClassifierFunc(errclass.New)

	// Create all the possible stages.
	connectStage := nop.NewConnectFunc(cfg, reqb.Protocol, logger)
	observeConnStage := nop.NewObserveConnFunc(cfg, logger)
	autoCancelStage := nop.NewCancelWatchFunc()
	tlsHandshakeStage := nop.NewTLSHandshakeFunc(cfg, tlsConfig, logger)
	httpConnStageCleartext := nop.NewHTTPConnFuncPlain(cfg, logger)
	httpConnStageTLS := nop.NewHTTPConnFuncTLS(cfg, logger)

	// Configure the pipeline timeout.
	ctx, cancel := context.WithTimeout(ctx, reqb.Timeout)
	defer cancel()

	// TODO(bassosimone): add here all the possible pipelines

	// Determine what to do depending on the `--pipeline <name>` flag.
	var bodyReader io.ReadCloser = io.NopCloser(strings.NewReader(""))
	switch reqb.Pipeline {
	case "https":
		dialPipe := nop.Compose5(
			connectStage,
			observeConnStage,
			autoCancelStage,
			tlsHandshakeStage,
			httpConnStageTLS,
		)

		// Dial the HTTPS connection.
		httpConn, err := dialPipe.Call(ctx, addrPort)
		if err != nil {
			logger.Error(
				"sondaFailure",
				slog.String("operation", "dial"),
				slog.Any("err", err),
				slog.Int("exitCode", 1),
			)
			return 1
		}
		defer httpConn.Close()

		// Perform the HTTP round trip.
		resp, err := httpConn.RoundTrip(httpReq)
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
		bodyReader = resp.Body

	case "http":
		dialPipe := nop.Compose4(
			connectStage,
			observeConnStage,
			autoCancelStage,
			httpConnStageCleartext,
		)

		// Dial the HTTP connection.
		httpConn, err := dialPipe.Call(ctx, addrPort)
		if err != nil {
			logger.Error(
				"sondaFailure",
				slog.String("operation", "dial"),
				slog.Any("err", err),
				slog.Int("exitCode", 1),
			)
			return 1
		}
		defer httpConn.Close()

		// Perform the HTTP round trip.
		resp, err := httpConn.RoundTrip(httpReq)
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
		bodyReader = resp.Body
	}

	// Drain the body to trigger body stream logging.
	if _, err := io.Copy(rla.body, bodyReader); err != nil {
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
