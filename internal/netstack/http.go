// SPDX-License-Identifier: GPL-3.0-or-later

package netstack

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/paths"
	"github.com/bassosimone/sonda/internal/structured"
)

// HTTPTransport performs HTTP/HTTPS round trips using [SondaMeasurer].
//
// Use [NewHTTPTransport] to construct.
type HTTPTransport struct {
	// Measurer is the measurement runner.
	//
	// Set by [NewHTTPTransport].
	Measurer *SondaMeasurer

	// Resolver resolves hostnames to IP addresses.
	//
	// Set by [NewHTTPTransport].
	Resolver *Resolver

	// Timeout is the timeout for each measurement.
	//
	// Default: 30s.
	Timeout time.Duration
}

// NewHTTPTransport creates a [*HTTPTransport] with sensible defaults.
func NewHTTPTransport(measurer *SondaMeasurer, resolver *Resolver) *HTTPTransport {
	return &HTTPTransport{
		Measurer: measurer,
		Resolver: resolver,
		Timeout:  30 * time.Second,
	}
}

var _ http.RoundTripper = &HTTPTransport{}

// RoundTrip implements [http.RoundTripper].
func (t *HTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Validate the scheme.
	scheme := req.URL.Scheme
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("httpTransport: unsupported scheme: %s", scheme)
	}

	// Validate the method.
	method := req.Method
	if method == "" {
		method = "GET"
	}
	if method != "GET" && method != "HEAD" {
		return nil, fmt.Errorf("httpTransport: unsupported method: %s", method)
	}

	// Convert request headers to "Key: Value" strings.
	var headers []string
	for key, values := range req.Header {
		for _, v := range values {
			headers = append(headers, key+": "+v)
		}
	}

	// Resolve the hostname.
	hostname := req.URL.Hostname()
	addrs, err := t.Resolver.LookupHost(req.Context(), hostname)
	if err != nil {
		return nil, err
	}

	// Determine the port.
	port := req.URL.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}

	// Determine the URL path.
	urlPath := req.URL.Path
	if urlPath == "" {
		urlPath = "/"
	}

	// Try each resolved address sequentially.
	var errs []error
	for _, addr := range addrs {
		target := net.JoinHostPort(addr, port)
		resp, err := t.roundTripAddr(req, method, target, urlPath, headers)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		return resp, nil
	}
	err = errors.Join(errs...)
	runtimex.Assert(err != nil)
	return nil, err
}

// roundTripAddr performs a single round trip to a specific address.
func (t *HTTPTransport) roundTripAddr(req *http.Request, method, target, urlPath string, headers []string) (*http.Response, error) {
	// Build the operation based on the URL scheme.
	bodyFile := "@SONDA_SPAN_DIR@/body.bin"
	httpHost := req.URL.Host
	var op SondaOperation

	switch scheme := req.URL.Scheme; scheme {
	case "http":
		op = &SondaMeasureHTTP{
			BodyFile: bodyFile,
			Headers:  headers,
			HTTPHost: httpHost,
			Method:   method,
			Target:   target,
			Timeout:  t.Timeout,
			URLPath:  urlPath,
		}

	default:
		runtimex.Assert(scheme == "https")
		op = &SondaMeasureHTTPS{
			BodyFile: bodyFile,
			Headers:  headers,
			HTTPHost: httpHost,
			Method:   method,
			SNI:      req.URL.Hostname(),
			Target:   target,
			Timeout:  t.Timeout,
			URLPath:  urlPath,
		}
	}

	// Run the measurement.
	spanDir, err := t.Measurer.Run(req.Context(), op)
	if err != nil {
		return nil, err
	}

	// Parse the response from the structured logs.
	return readHTTPResponse(req, spanDir)
}

// readHTTPResponse reads the structured logs from a span directory and
// constructs an [*http.Response].
func readHTTPResponse(req *http.Request, spanDir string) (*http.Response, error) {
	statusCode, headers, err := readHTTPRoundTripDone(spanDir)
	if err != nil {
		return nil, err
	}

	// Open the body file if it exists.
	var body io.ReadCloser = http.NoBody
	if filep, err := os.Open(paths.SpanBodyBin(spanDir)); err == nil {
		body = filep
	}

	resp := &http.Response{
		StatusCode: statusCode,
		Header:     headers,
		Body:       body,
		Request:    req,
	}
	return resp, nil
}

// readHTTPRoundTripDone reads stdout.txt from a span directory and extracts
// the status code and response headers from the httpRoundTripDone message.
func readHTTPRoundTripDone(spanDir string) (int, http.Header, error) {
	filep, err := os.Open(paths.SpanStdout(spanDir))
	if err != nil {
		return 0, nil, err
	}
	defer filep.Close()

	scanner := bufio.NewScanner(filep)
	for scanner.Scan() {
		ev, err := structured.ParseEvent(scanner.Bytes())
		if err != nil {
			continue
		}
		if ev.Msg != "httpRoundTripDone" {
			continue
		}
		if ev.HTTPResponseStatusCode == 0 {
			return 0, nil, errors.New("httpRoundTripDone: missing status code")
		}
		headers := ev.HTTPResponseHeaders
		if headers == nil {
			headers = make(http.Header)
		}
		return ev.HTTPResponseStatusCode, headers, nil
	}

	if err := scanner.Err(); err != nil {
		return 0, nil, err
	}
	return 0, nil, errors.New("no httpRoundTripDone message found")
}
