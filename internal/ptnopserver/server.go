// SPDX-License-Identifier: GPL-3.0-or-later

// Package ptnopserver implements the ptnoprpc server.
package ptnopserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/ptnoprpc"
	"github.com/bassosimone/sonda/internal/spool"
	"github.com/bassosimone/sonda/internal/testable"
)

const (
	// defaultTimeout is the pipeline timeout used when the request does not
	// specify one. Same as the `sonda measure-ptnop` default.
	defaultTimeout = 30 * time.Second

	// maxTimeout is the maximum pipeline timeout a request may ask for, so
	// that a single request cannot hold the connection for too long. Same
	// as the `sonda spool-run` default.
	maxTimeout = 5 * time.Minute
)

// ServeConn reads requests from the conn (a single JSON per line), runs the
// corresponding pipeline and writes the response when done.
//
// Takes ownership of conn and closes it when done. Note that when the context
// is canceled, the conn is automatically closed.
func ServeConn(
	ctx context.Context,
	conn net.Conn,
	env *testable.Environ,
	idleTimeout time.Duration,
	logger *slog.Logger,
	spoolRootDir spool.RootDir,
) {
	// Close the connection when we leave the scope or the service-scoped context is done.
	defer conn.Close()
	stopCloser := context.AfterFunc(ctx, func() {
		conn.Close()
	})
	defer stopCloser()

	// Get the peer credentials.
	reader := &idleReader{conn: conn, timeout: idleTimeout}
	writer := &idleWriter{conn: conn, timeout: idleTimeout}
	peer := unknownPeerCreds
	if uconn, ok := conn.(*net.UnixConn); ok {
		peer, _ = peerCred(uconn) // best effort + portability on Windows/macOS
	}

	// Read and serve incoming requests, one per line, sequentially.
	//
	// Each request receives exactly one response line. A malformed or invalid
	// request receives an error response and does not close the connection.
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(nil, ptnoprpc.MaxLineSize)
	for scanner.Scan() {
		resp := serveLine(ctx, env, logger, scanner.Bytes(), peer, spoolRootDir)
		respData := runtimex.PanicOnError1(json.Marshal(resp)) // always serializable
		respData = append(respData, '\n')
		if _, err := writer.Write(respData); err != nil {
			logger.Warn("writer.Write", slog.Any("err", err))
			return // the client is gone or not reading
		}
	}

	// Note: an idle client or a line longer than [ptnoprpc.MaxLineSize]
	// ends the connection here.
	if err := scanner.Err(); err != nil {
		logger.Warn("scanner.Err", slog.Any("err", err))
	}
}

// serveLine serves a raw request line received from the conn and
// returns the response to send back. It never returns nil.
//
// The logger is the operational logger (i.e., the journal). The measurement
// events go to the span's `stdout.txt.gz` file instead.
//
// The peer contains the client credentials, possibly [unknownPeerCreds].
func serveLine(
	ctx context.Context,
	env *testable.Environ,
	logger *slog.Logger,
	rawLine []byte,
	peer peerCreds,
	spoolRootDir spool.RootDir,
) *ptnoprpc.Response {
	// 1. Parse the raw line received from the client.
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

	// 4. Create a writable span for writing the measurement results.
	spanDir, err := spool.CreateTempSpanDir(spoolRootDir, "ptnop")
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	defer spanDir.Close() // ensure cleanup on failure path
	spanID := spanDir.ID()
	resp.SpanID = spanID

	// From now on, failures are failures of the server rather than of the
	// request, hence we also log them to the journal.
	serverFailure := func(operation string, err error) *ptnoprpc.Response {
		logger.Error(operation, slog.String("spanId", spanID), slog.Any("err", err))
		resp.Error = err.Error()
		return resp
	}

	// 5. Create an entity representing the stdout in the writable span.
	stdoutWriter, err := spanDir.CreateEntity("stdout.txt")
	if err != nil {
		return serverFailure("spanDir.CreateEntity", err)
	}

	// 6. Same as above for the body, if needed.
	switch req.Pipeline {
	case "http", "https":
		if req.HTTPBodyFile {
			bodyWriter, err := spanDir.CreateEntity("body.bin")
			if err != nil {
				return serverFailure("spanDir.CreateEntity", err)
			}
			input.bodyFp = bodyWriter
		}
	}

	// 7. Create the structured logger writing the measurement events.
	input.logger = newServeLogger(stdoutWriter, spanID, req.Tags, req.Optimize)

	// 8. Write peer credentials and incoming request.
	input.logger.Info("sondaPtnopRunContext", slog.Any("peer", peer), slog.Any("request", req))

	// 9. Run the pipeline.
	exitCode := runPipeline(ctx, input)

	// 10. Make sure we properly publish the temporary span.
	if err := spanDir.Publish(); err != nil {
		return serverFailure("spanDir.Publish", err)
	}

	// 11. Tell the caller that we succeeded.
	resp.ExitCode = &exitCode
	return resp
}

// newServeLogger creates the JSON logger writing to w and bound to tags. When optimize
// is "size", we trim the emitted events as documented by [*ptnoprpc.Request].
//
// When optimize is "speed" we keep INFO events and decimated readDone and writeDone
// events, so that it is still possible to compute speed stats.
//
// We omit the top-level time field: library events carry their own times.
func newServeLogger(w io.Writer, spanID string, tags []string, optimize string) pipelineSLogger {
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

	// When optimizing for speed, use the decimating logger.
	switch optimize {
	case "speed":
		return newDecimateLogger(logger)
	default:
		return logger
	}
}
