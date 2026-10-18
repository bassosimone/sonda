// SPDX-License-Identifier: GPL-3.0-or-later

// Package ptnoprpc contains the wire format spoken over the `sonda-serve-ptnop`
// socket. Each request and each response is a JSON object on a single line.
//
// The server reads one request and writes exactly one response, in order,
// until the client closes the connection.
package ptnoprpc

import (
	"encoding/json"
	"time"
)

// MaxLineSize bounds the size of a request or response line. Requests/responses are small
// JSON objects, so this is generous. Used by the client and the server.
const MaxLineSize = 1 << 19

// UsageErrorPrefix is the prefix the server adds to errors
// that indicate that a usage error occurred.
const UsageErrorPrefix = "usage error: "

// Request is the request sent by the client.
//
// # ID
//
// The ID is opaque: the server echoes it back in the [Response], so the client
// can match responses to requests. A missing ID is echoed as a missing ID.
//
// # Timeout
//
// A Timeout <= 0 means the server default.
//
// # Optimize
//
// The Optimize field indicates what we should optimize for in terms of the
// generated events. When Optimize is empty, the `serve-ptnop` service collects
// and emits all structured log events. Set to "size" to optimize for lean
// output. Specifics may change in the future, and some sonda events may
// be trimmed as well. What matters is that "size" guarantees that at least
// the following events are present, with all their keys:
//
//   - closeDone
//   - connectDone
//   - dnsExchangeDone
//   - dnsResponse
//   - httpBodyStreamDone
//   - httpRoundTripDone
//   - sondaDnsRecordsA
//   - sondaDnsRecordsAAAA
//   - sondaPtnopRunContext
//   - stunBindingResult
//   - tlsHandshakeDone
//
// With these events and their full keys, one should still be able to figure
// out what happened, and yet the output file is leaner.
//
// Any unknown value is silently equal to empty and emits everything.
//
// # MaxHTTPResponseBodySize
//
// The MaxHTTPResponseBodySize optional setting controls whether we
// should cap the maximum response body size. A zero (or negative) value
// disables this functionality. Otherwise, we stop reading the response
// body after the given number of bytes has been read. When scanning
// multiple URLs that may have relatively large bodies, it is advisable
// to use this functionality, to limit the overall network I/O.
//
// # Spool
//
// Set Spool to "service" to buffer gzip-compressed structured logs
// and (when enabled) the body, and submit them to the `sonda-serve-spool`
// service when the pipeline terminates.
//
// Set to "fs" or "" to write the spoolDir directly.
//
// Any unrecognized value is silently mapped to "fs" without warnings.
//
// The "fs" strategy uses less memory but does disk I/O. With this
// strategy, the structured logs and the body are streamed directly to
// disk; the only limiting factors are the disk size and wear.
//
// With "service", `sonda-serve-spool` refuses submissions larger
// than ~16 MiB (by default). The submission is kept in memory until
// it is garbage collected and `sonda-serve-spool` refuses
// new submissions until garbage collection when it is already
// buffering 256 MiB of data (by default). If your measurements
// are small and you do not want any disk I/O, use "service".
type Request struct {
	ID                      json.RawMessage `json:"id,omitempty"`
	ALPN                    []string        `json:"alpn"`
	AddrPort                string          `json:"addrPort"`
	DNSQueryName            string          `json:"dnsQueryName"`
	DNSQueryType            string          `json:"dnsQueryType"`
	HTTPBodyFile            bool            `json:"httpBodyFile"`
	HTTPHeaders             []string        `json:"httpHeaders"`
	HTTPHost                string          `json:"httpHost"`
	HTTPMethod              string          `json:"httpMethod"`
	HTTPScheme              string          `json:"httpScheme"`
	MaxHTTPResponseBodySize int64           `json:"maxHttpResponseBodySize"`
	Optimize                string          `json:"optimize"`
	Pipeline                string          `json:"pipeline"`
	SNI                     string          `json:"sni"`
	Spool                   string          `json:"spool"`
	Tags                    []string        `json:"tags"`
	Timeout                 time.Duration   `json:"timeout"`
	URLPath                 string          `json:"urlPath"`
	URLQuery                string          `json:"urlQuery"`
}

// Response is the response sent by the server.
//
// On success, Error is empty and SpanID and ExitCode are set. The ExitCode is
// either `0` (success) or `1` (measurement failure).
//
// On failure, Error is set and ExitCode is nil. SpanID is set if and only if
// the failure happened after the server started creating the span directory, in
// which case a `.tmp` directory may be left behind for `sonda spool-gc` to remove.
//
// When the server detects a usage error (e.g., an invalid pipeline name), it
// creates an Error string starting with [UsageErrorPrefix] so that [*Client]
// can return a [UsageError].
type Response struct {
	ID       json.RawMessage `json:"id,omitempty"`
	Error    string          `json:"error,omitempty"`
	ExitCode *int            `json:"exitCode,omitempty"`
	SpanID   string          `json:"spanId,omitempty"`
}
