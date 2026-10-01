// SPDX-License-Identifier: GPL-3.0-or-later.

// Package nob contains common code for the network observation backend plugin.
package nob

import "time"

// MaxBodySize is the maximum body size.
const MaxBodySize = 1 << 19

const (
	// BodyBinName is the name of the `body.bin` file.
	BodyBinName = "body.bin"

	// ExitcodeTxtName is the name of the `exitcode.txt` file.
	ExitcodeTxtName = "exitcode.txt"

	// RequestJsonName is the name of the `request.json` file.
	RequestJsonName = "request.json"

	// StderrTxtName is the name of the `stderr.txt` file.
	StderrTxtName = "stderr.txt"

	// StdoutJsonName is the name of the `stdout.json` file.
	StdoutJsonName = "stdout.json"
)

// GCRequestBody is the request body for `POST /api/v1/gc`.
type GCRequestBody struct {
	MaxAge time.Duration `json:"maxAge"`
}

// GCResponseBody is the response body for `POST /api/v1/gc`.
type GCResponseBody struct{}

// RunRequestBody is the request body for `POST /api/v1/run`.
type RunRequestBody struct {
	ALPN         []string      `json:"alpn"`
	AddrPort     string        `json:"addrPort"`
	DNSQueryName string        `json:"dnsQueryName"`
	DNSQueryType string        `json:"dnsQueryType"`
	HTTPHeaders  []string      `json:"httpHeaders"`
	HTTPHost     string        `json:"httpHost"`
	HTTPMethod   string        `json:"httpMethod"`
	HTTPScheme   string        `json:"httpScheme"`
	Pipeline     string        `json:"pipeline"`
	SNI          string        `json:"sni"`
	Tags         []string      `json:"tags"`
	Timeout      time.Duration `json:"timeout"`
	URLPath      string        `json:"urlPath"`
}

// RunResponseBody is the response body returned by `POST /api/v1/run`.
type RunResponseBody struct {
	SpanID string `json:"spanID"`
}

// DefaultSocketPath is the default Unix socket path to use.
const DefaultSocketPath = "/var/run/sonda/nob/nob.sock"
