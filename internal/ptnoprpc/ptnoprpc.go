// SPDX-License-Identifier: GPL-3.0-or-later

// Package ptnoprpc contains the wire format spoken over the `sonda-inetd-ptnop`
// socket. Each request and each response is a JSON object on a single line.
//
// The server reads one request and writes exactly one response, in order,
// until the client closes the connection.
package ptnoprpc

import (
	"encoding/json"
	"time"
)

// Request is the request sent by the client.
//
// The ID is opaque: the server echoes it back in the [Response], so the client
// can match responses to requests. A missing ID is echoed as a missing ID.
//
// A Timeout <= 0 means the server default.
type Request struct {
	ID           json.RawMessage `json:"id,omitempty"`
	ALPN         []string        `json:"alpn"`
	AddrPort     string          `json:"addrPort"`
	DNSQueryName string          `json:"dnsQueryName"`
	DNSQueryType string          `json:"dnsQueryType"`
	HTTPBodyFile bool            `json:"httpBodyFile"`
	HTTPHeaders  []string        `json:"httpHeaders"`
	HTTPHost     string          `json:"httpHost"`
	HTTPMethod   string          `json:"httpMethod"`
	HTTPScheme   string          `json:"httpScheme"`
	Pipeline     string          `json:"pipeline"`
	SNI          string          `json:"sni"`
	Tags         []string        `json:"tags"`
	Timeout      time.Duration   `json:"timeout"`
	URLPath      string          `json:"urlPath"`
}

// Response is the response sent by the server.
//
// On success, Error is empty and SpanID, SpanDir, and ExitCode are set. The
// ExitCode is either `0` (success) or `1` (measurement failure).
//
// On failure, Error is set and ExitCode is nil. SpanID is set if and only if
// the failure happened after the server started creating the span directory, in
// which case a `.tmp` directory may be left behind for `sonda spool gc` to remove.
//
// When the server detects a usage error (e.g., an invalid pipeline name), it
// creates an Error string starting with [UsageErrorPrefix] so that [*Client]
// can return a [UsageError].
type Response struct {
	ID       json.RawMessage `json:"id,omitempty"`
	Error    string          `json:"error,omitempty"`
	ExitCode *int            `json:"exitCode,omitempty"`
	SpanDir  string          `json:"spanDir,omitempty"`
	SpanID   string          `json:"spanId,omitempty"`
}
