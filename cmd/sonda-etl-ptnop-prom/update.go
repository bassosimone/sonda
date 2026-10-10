// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"log/slog"
	"os"
	"time"

	"github.com/bassosimone/sonda/internal/ptnopdata"
	"github.com/bassosimone/sonda/internal/ptnoppaths"
	"github.com/bassosimone/sonda/internal/testable"
)

// updateMaxLineSize is the maximum accepted line size.
const updateMaxLineSize = 1 << 19

// updateUnifiedEvent is the unified event used to update Prometheus metrics.
type updateUnifiedEvent struct {
	SpanID string

	LocalAddr  string
	RemoteAddr string
	Protocol   string

	ReflexiveAddrV4 string
	ReflexiveAddrV6 string

	ConnectDuration        time.Duration
	DNSExchangeDuration    time.Duration
	HTTPBodyStreamDuration time.Duration
	HTTPRoundTripDuration  time.Duration
	TLSHandshakeDuration   time.Duration

	ConnectErr        string
	DNSExchangeErr    string
	HTTPBodyStreamErr string
	HTTPRoundTripErr  string
	TLSHandshakeErr   string

	TLSServerName         string
	TLSCipherSuite        string
	TLSNegotiatedProtocol string
	TLSVersion            string

	HTTPRequestMethod      string
	HTTPRequestUrl         string
	HTTPResponseStatusCode int

	DNSQueryName      string
	DNSQueryType      string
	DNSServerProtocol string

	ReadBytes  int64
	WriteBytes int64

	// FailedAt is the `msg` of the first stage that failed with an error
	// other than `ESKIP`, or empty if no stage failed.
	FailedAt string

	// FailedErr is the `errClass` of the FailedAt stage.
	FailedErr string
}

// maybeSetFailedAt sets the name of the first event message that failed thus
// not considering "ESKIP" failures into the pipeline failure computation.
func (uue *updateUnifiedEvent) maybeSetFailedAt(ev *ptnopdata.Event) {
	if uue.FailedAt == "" && ev.ErrClass != "" && ev.ErrClass != "ESKIP" {
		uue.FailedAt = ev.Msg
		uue.FailedErr = ev.ErrClass
	}
}

// Update updates the [*updateUnifiedEvent] using the given [*ptnopdata.Event].
func (uue *updateUnifiedEvent) Update(ev *ptnopdata.Event) {
	uue.SpanID = ev.SpanID
	if ev.LocalAddr != "" {
		uue.LocalAddr = ev.LocalAddr
	}
	if ev.RemoteAddr != "" {
		uue.RemoteAddr = ev.RemoteAddr
	}
	if ev.Protocol != "" {
		uue.Protocol = ev.Protocol
	}
	if ev.ReflexiveAddrV4 != "" {
		uue.ReflexiveAddrV4 = ev.ReflexiveAddrV4
	}
	if ev.ReflexiveAddrV6 != "" {
		uue.ReflexiveAddrV6 = ev.ReflexiveAddrV6
	}

	switch ev.Msg {
	case "connectDone":
		uue.ConnectDuration = ev.T.Sub(ev.T0)
		uue.ConnectErr = ev.ErrClass
		uue.maybeSetFailedAt(ev)

	case "dnsExchangeDone":
		uue.DNSExchangeDuration = ev.T.Sub(ev.T0)
		uue.DNSExchangeErr = ev.ErrClass
		uue.maybeSetFailedAt(ev)
		uue.DNSQueryName = ev.DNSQueryName
		uue.DNSQueryType = ev.DNSQueryType
		uue.DNSServerProtocol = ev.DNSServerProtocol

	case "httpBodyStreamDone":
		uue.HTTPBodyStreamDuration = ev.T.Sub(ev.T0)
		uue.HTTPBodyStreamErr = ev.ErrClass
		uue.maybeSetFailedAt(ev)

	case "httpRoundTripDone":
		uue.HTTPRoundTripDuration = ev.T.Sub(ev.T0)
		uue.HTTPRoundTripErr = ev.ErrClass
		uue.maybeSetFailedAt(ev)
		uue.HTTPRequestMethod = ev.HTTPRequestMethod
		uue.HTTPRequestUrl = ev.HTTPRequestUrl
		uue.HTTPResponseStatusCode = ev.HTTPResponseStatusCode

	case "closeDone":
		uue.ReadBytes = ev.IOTotalBytesRead
		uue.WriteBytes = ev.IOTotalBytesWritten

	case "tlsHandshakeDone":
		uue.TLSHandshakeDuration = ev.T.Sub(ev.T0)
		uue.TLSHandshakeErr = ev.ErrClass
		uue.maybeSetFailedAt(ev)
		uue.TLSServerName = ev.TLSServerName
		uue.TLSCipherSuite = ev.TLSCipherSuite
		uue.TLSNegotiatedProtocol = ev.TLSNegotiatedProtocol
		uue.TLSVersion = ev.TLSVersion
	}
}

// updateMetrics updates Prometheus metrics based on the spanDir content.
//
// Returns the number of processed events and/or an error.
func updateMetrics(
	env *testable.Environ,
	logger *slog.Logger,
	spanDir string,
) (int64, error) {
	// Open the source file: `stdout.txt` inside the current span dir.
	inputPath := ptnoppaths.SpanStdout(spanDir)
	source, err := env.OpenFile(inputPath, os.O_RDONLY, 0)
	if err != nil {
		return 0, err
	}
	defer source.Close()

	// Create the scanner.
	scanner := bufio.NewScanner(source)
	scanner.Buffer(nil, updateMaxLineSize)

	// Walk through all input lines.
	var (
		lineID       int64
		numProcessed int64
		unified      = &updateUnifiedEvent{}
	)
	for scanner.Scan() {
		line := scanner.Bytes()
		lineID += 1
		if len(line) <= 0 {
			continue
		}

		ev, err := ptnopdata.ParseEvent(line)
		if err != nil {
			logger.Warn(
				"ptnopdata.ParseEvent",
				slog.Any("err", err),
				slog.String("file", inputPath),
				slog.Int64("line", lineID),
			)
			continue
		}

		numProcessed += 1
		unified.Update(ev)
	}

	// Bail if we could not read the whole file.
	if err := scanner.Err(); err != nil {
		return numProcessed, err
	}

	// Update Prometheus metrics and return.
	// TODO(bassosimone): implement
	return numProcessed, nil
}
