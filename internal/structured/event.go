// SPDX-License-Identifier: GPL-3.0-or-later

// Package structured defines the union type for all structured log events
// emitted by sonda measurement commands.
package structured

import (
	"encoding/json"
	"net/http"
	"time"
)

// Failure is a nullable error description. A nil *Failure means success;
// a non-nil *Failure holds the error string. Serializes as JSON null or string.
type Failure string

// Event is the union of all fields emitted by nop (pipeline layer) and
// sonda (command layer) structured log events. Each event populates a
// subset of fields; the Msg field identifies which subset is meaningful.
type Event struct {
	// --- slog standard fields (all events) ---

	Time  time.Time `json:"time"`
	Level string    `json:"level"`
	Msg   string    `json:"msg"`

	// --- sonda command layer (logger.With context) ---

	SpanID string `json:"spanID,omitempty"`

	// --- nop pipeline: common fields ---
	// Used by: connectStart/Done, closeStart/Done, tlsHandshakeStart/Done,
	// httpRoundTripStart/Done, httpBodyStreamStart/Done, dnsExchangeStart/Done,
	// readStart/Done, writeStart/Done, setDeadline, setReadDeadline, setWriteDeadline

	T          time.Time `json:"t"`
	T0         time.Time `json:"t0"`
	Deadline   time.Time `json:"deadline"`
	LocalAddr  string    `json:"localAddr,omitempty"`
	RemoteAddr string    `json:"remoteAddr,omitempty"`
	Protocol   string    `json:"protocol,omitempty"`

	// --- nop pipeline: error fields ---
	// Used by: connectDone, closeDone, tlsHandshakeDone, httpRoundTripDone,
	// httpBodyStreamDone, dnsExchangeDone, readDone, writeDone, sondaFailure

	Err      *Failure `json:"err,omitempty"`
	ErrClass string   `json:"errClass,omitempty"`

	// --- sonda command layer: failure ---
	// Used by: sondaFailure

	Operation string `json:"operation,omitempty"`

	// --- nop pipeline: I/O fields ---
	// Used by: readStart/Done, writeStart/Done

	IOBufferSize int `json:"ioBufferSize,omitempty"`
	IOBytesCount int `json:"ioBytesCount,omitempty"`

	// --- nop pipeline: TLS fields ---
	// Used by: tlsHandshakeStart/Done

	TLSEngineName         string   `json:"tlsEngineName,omitempty"`
	TLSParrot             string   `json:"tlsParrot,omitempty"`
	TLSOfferedProtocols   []string `json:"tlsOfferedProtocols,omitempty"`
	TLSServerName         string   `json:"tlsServerName,omitempty"`
	TLSSkipVerify         bool     `json:"tlsSkipVerify,omitempty"`
	TLSCipherSuite        string   `json:"tlsCipherSuite,omitempty"`
	TLSNegotiatedProtocol string   `json:"tlsNegotiatedProtocol,omitempty"`
	TLSPeerCerts          [][]byte `json:"tlsPeerCerts,omitempty"`
	TLSVersion            string   `json:"tlsVersion,omitempty"`

	// --- nop pipeline: HTTP fields ---
	// Used by: httpRoundTripStart/Done

	HTTPMethod             string      `json:"httpMethod,omitempty"`
	HTTPUrl                string      `json:"httpUrl,omitempty"`
	HTTPRequestHeaders     http.Header `json:"httpRequestHeaders,omitempty"`
	HTTPResponseHeaders    http.Header `json:"httpResponseHeaders,omitempty"`
	HTTPResponseStatusCode int         `json:"httpResponseStatusCode,omitempty"`

	// --- nop pipeline: DNS fields ---
	// Used by: dnsExchangeStart/Done, dnsQuery, dnsResponse

	ServerProtocol string `json:"serverProtocol,omitempty"`
	DNSRawQuery    []byte `json:"dnsRawQuery,omitempty"`
	DNSRawResponse []byte `json:"dnsRawResponse,omitempty"`

	// --- sonda command layer: lifecycle ---
	// Used by: sondaCommandLineArgs

	CLIArgs []string `json:"cliArgs,omitempty"`

	// --- sonda command layer: HTTP response ---
	// Used by: sondaHttpResponseBodyStats

	HTTPResponseBodySize int64 `json:"httpResponseBodySize,omitempty"`

	// --- sonda command layer: STUN ---
	// Used by: stunBindingResult

	STUNReflexiveAddr string `json:"stunReflexiveAddr,omitempty"`
	STUNReflexivePort int    `json:"stunReflexivePort,omitempty"`

	// --- sonda command layer: DNS response ---
	// Used by: sondaDnsRecordsA, sondaDnsRecordsAAAA, sondaDnsRecordsCNAME

	DNSRecordsList []string `json:"dnsRecordsList,omitempty"`

	// --- sonda command layer: contextual tags ---
	// Used by: all events (injected via --tag)

	ReflexiveAddrV4 string `json:"reflexiveAddrV4,omitempty"`
	ReflexiveAddrV6 string `json:"reflexiveAddrV6,omitempty"`
}

// ParseEvent parses a single JSON structured log line into an [*Event].
func ParseEvent(line []byte) (*Event, error) {
	var ev Event
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, err
	}
	return &ev, nil
}
