// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
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
	"github.com/bassosimone/ptnop"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/plugins/nob"
	"github.com/google/uuid"
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
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxRequestBodySize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reqb); err != nil {
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
	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	logger = logger.With("spanID", spanID)

	for _, tag := range tags {
		if key, value, ok := strings.Cut(tag, "="); ok {
			logger = logger.With(key, value)
		}
	}

	return logger
}

func runNewHTTPRequest(ctx context.Context, reqb *runRequestBody) (*http.Request, error) {
	httpURL := (&url.URL{
		Scheme: reqb.HTTPScheme,
		Host:   reqb.HTTPHost,
		Path:   reqb.URLPath,
	}).String()

	httpReq, err := http.NewRequestWithContext(ctx, reqb.HTTPMethod, httpURL, http.NoBody)
	if err != nil {
		return nil, err
	}

	for _, h := range reqb.HTTPHeaders {
		key, value, ok := strings.Cut(h, ":")
		if !ok {
			return nil, fmt.Errorf("missing colon in header: %s", h)
		}
		httpReq.Header.Add(strings.TrimSpace(key), strings.TrimSpace(value))
	}

	return httpReq, nil
}

// runPipeline runs a measurement pipeline.
func (h *handler) runPipeline(ctx context.Context, rla *runLocalArgs, reqb *runRequestBody) int {
	// Emit structured logs to the stdout tied together by the span ID.
	logger := runNewSlogLogger(rla.stdout, rla.spanID, reqb.Tags)

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
	httpReq, err := runNewHTTPRequest(ctx, reqb)
	if err != nil {
		logger.Error(
			"sondaFailure",
			slog.String("operation", "newHttpRequest"),
			slog.Any("err", err),
			slog.Int("exitCode", 2),
		)
		return 2
	}

	// Create the shared pipeline configuration.
	cfg := ptnop.NewConfig()
	cfg.SLogger = logger
	cfg.Dialer = h.env.Dialer

	// Create all the possible stages.
	connectStage := ptnop.NewConnectFunc(cfg, reqb.Protocol)
	observeConnStage := ptnop.NewObserveConnFunc(cfg)
	autoCancelStage := ptnop.NewCancelWatchFunc()
	tlsHandshakeStage := ptnop.NewTLSHandshakeFunc(cfg, tlsConfig)
	httpConnStage := ptnop.NewHTTPConnFunc(cfg)

	// Configure the pipeline timeout.
	ctx, cancel := context.WithTimeout(ctx, reqb.Timeout)
	defer cancel()

	// TODO(bassosimone): add here all the possible pipelines

	// Determine what to do depending on the `--pipeline <name>` flag.
	var bodyReader io.ReadCloser = io.NopCloser(strings.NewReader(""))
	switch reqb.Pipeline {
	case "https":
		dialPipe := ptnop.Compose5(
			connectStage,
			observeConnStage,
			autoCancelStage,
			tlsHandshakeStage,
			httpConnStage,
		)

		// Dial the HTTPS connection.
		httpConn := dialPipe.Call(ctx, addrPort)
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
		dialPipe := ptnop.Compose4(
			connectStage,
			observeConnStage,
			autoCancelStage,
			httpConnStage,
		)

		// Dial the HTTP connection.
		httpConn := dialPipe.Call(ctx, addrPort)
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
