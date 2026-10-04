// SPDX-License-Identifier: GPL-3.0-or-later

package ptnoprpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/ptnopspool"
	"github.com/bassosimone/sonda/internal/testable"
)

const (
	// maxLineSize bounds the size of a response line. Responses are small
	// JSON objects, so this is generous. Same as the server's request limit.
	maxLineSize = 1 << 19

	// maxRequestTimeout is the timeout we assume when [Request.Timeout] is
	// <= 0, since we do not know the server default. Same as the maximum
	// timeout accepted by `sonda-inetd-ptnop`.
	maxRequestTimeout = 5 * time.Minute

	// roundTripMargin is the time we allow the server, on top of the request
	// timeout, to create the span and send the response. This is a guess that
	// should leave plenty of margin for local file system operations.
	roundTripMargin = 10 * time.Second
)

// Client is a client for the `sonda-inetd-ptnop` socket.
//
// Construct using [Dial].
//
// A [*Client] runs one request at a time and is not safe for concurrent use.
//
// After a transport or protocol error, the connection is out of sync with the
// server (e.g., a late response could still arrive), so every subsequent
// [*Client.Run] fails with the same error. We do not reconnect.
type Client struct {
	conn    net.Conn
	env     *testable.Environ
	err     error
	nextID  int64
	scanner *bufio.Scanner
}

// Dial connects to the `sonda-inetd-ptnop` Unix domain socket at the given path.
func Dial(ctx context.Context, env *testable.Environ, path string) (*Client, error) {
	conn, err := env.Dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(nil, maxLineSize)
	client := &Client{
		conn:    conn,
		env:     env,
		err:     nil,
		nextID:  0,
		scanner: scanner,
	}
	return client, nil
}

// Close closes the connection with the server.
func (c *Client) Close() error {
	return c.conn.Close()
}

// UsageErrorPrefix is the prefix the server adds to errors
// that indicate that a usage error occurred.
const UsageErrorPrefix = "usage error: "

// UsageError wraps a remote error starting with [UsageErrorPrefix].
type UsageError struct {
	Err error
}

// Error returns the error as a string.
func (e UsageError) Error() string {
	return e.Err.Error()
}

// Unwrap returns the underlying error.
func (e UsageError) Unwrap() error {
	return e.Err
}

// Run sends the request, waits for the response, and returns the [*ptnopspool.SpanDir].
//
// We do not mutate the request. We send a copy with the ID replaced by our own
// counter, which we use to check that the response matches the request.
//
// The returned SpanDir ExitCode is either `0` (success) or `1` (measurement failure).
//
// We fail if the server reports an error or returns an invalid response, which do
// not break the connection, or on transport errors or when the response does not
// match the request, which do (see [Client]).
//
// The error is [UsageError] when the server noticed that the request contained invalid
// fields such as, for example, an unknown pipeline name or an invalid addrport.
func (c *Client) Run(ctx context.Context, req *Request) (*ptnopspool.SpanDir, error) {
	// 1. Refuse to use a connection that is out of sync.
	if c.err != nil {
		return nil, c.err
	}

	// 2. Copy the request and assign our own ID.
	c.nextID++
	id := json.RawMessage(strconv.FormatInt(c.nextID, 10))
	reqCopy := *req
	reqCopy.ID = id

	// 3. Bound the round trip using the request timeout plus a margin, or the
	// context deadline if earlier, and interrupt any I/O if the context is done.
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = maxRequestTimeout
	}
	deadline := time.Now().Add(timeout + roundTripMargin)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		return nil, c.fail(err)
	}
	stop := context.AfterFunc(ctx, func() {
		c.conn.SetDeadline(time.Unix(1, 0)) // in the past: interrupts pending I/O
	})
	defer stop()

	// 4. Send the request.
	reqData := runtimex.PanicOnError1(json.Marshal(&reqCopy)) // always serializable
	reqData = append(reqData, '\n')
	if _, err := c.conn.Write(reqData); err != nil {
		return nil, c.fail(err)
	}

	// 5. Receive the response.
	if !c.scanner.Scan() {
		err := c.scanner.Err()
		if err == nil {
			err = io.ErrUnexpectedEOF // the server closed the connection
		}
		return nil, c.fail(err)
	}
	var resp Response
	if err := json.Unmarshal(c.scanner.Bytes(), &resp); err != nil {
		return nil, c.fail(err)
	}

	// 6. Make sure the response matches the request.
	if !bytes.Equal(resp.ID, id) {
		return nil, c.fail(fmt.Errorf("ptnoprpc: expected response ID %s, got %q", id, resp.ID))
	}

	// 7. Handle the errors reported by the server.
	if resp.Error != "" {
		err := fmt.Errorf("ptnoprpc: server error: %s", resp.Error)
		if strings.HasPrefix(resp.Error, UsageErrorPrefix) {
			err = UsageError{err}
		}
		return nil, err
	}
	if resp.ExitCode == nil {
		return nil, errors.New("ptnoprpc: missing exitCode in response")
	}
	if resp.SpanDir == "" {
		return nil, errors.New("ptnoprpc: missing spanDir in response")
	}
	if *resp.ExitCode != 0 && *resp.ExitCode != 1 {
		return nil, fmt.Errorf("ptnoprpc: unexpected exit code %d", *resp.ExitCode)
	}

	// 8. On success, return the span directory.
	spanDir := &ptnopspool.SpanDir{
		Env:      c.env,
		ExitCode: *resp.ExitCode,
		Path:     resp.SpanDir,
		SpanID:   resp.SpanID,
	}
	return spanDir, nil
}

// fail records a transport or protocol error, which makes the connection
// unusable for subsequent requests, and returns it.
func (c *Client) fail(err error) error {
	c.err = fmt.Errorf("ptnoprpc: connection unusable: %w", err)
	return c.err
}
