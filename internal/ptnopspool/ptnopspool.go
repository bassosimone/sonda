// SPDX-License-Identifier: GPL-3.0-or-later

// Package ptnopspool contains code to interact with the ptnop spool.
package ptnopspool

import (
	"bufio"
	"errors"
	"os"

	"github.com/bassosimone/sonda/internal/ptnopdata"
	"github.com/bassosimone/sonda/internal/ptnoppaths"
	"github.com/bassosimone/sonda/internal/testable"
)

// maxLineSize is large enough to contain a DoH response (max size 64 KiB) encoded in
// base64 (87 KiB) or pathologically large response headers.
const maxLineSize = 1 << 19

// ErrNoData is the error returned when a specific span directory does not
// contain any data relevant for the requested operation.
//
// For example: you want the reflexive IP address but there is no record
// inside the span's structured logs about it.
var ErrNoData = errors.New("ptnopspool: no data")

// ReadError is an error indicating that we cannot open, scan, or parse
// the structured logs contained within a specific span dir.
type ReadError struct {
	Err error
}

// Error returns a string representation of the error.
func (e ReadError) Error() string {
	return e.Err.Error()
}

// Unwrap returns the underlying error.
func (e ReadError) Unwrap() error {
	return e.Err
}

// SpanDir is the directory containing the result of running a pipeline.
//
// The ExitCode is either `0` (success) or `1` (measurement error).
//
// The Path is the span dir path.
type SpanDir struct {
	Env      *testable.Environ
	ExitCode int
	Path     string
}

// ResolvedAddrsA returns the resolved IPv4 addrs.
//
// Failure modes:
//
//  1. [ErrNoData] if we cannot find the matching structured logs
//
//  2. [ReadError] if we cannot open, scan, or parse the structured logs file
func (d *SpanDir) ResolvedAddrsA() ([]string, error) {
	return d.resolvedAddrs("sondaDnsRecordsA")
}

// ResolvedAddrsAAAA is like [*SpanDir.ResolvedAddrsA] but for AAAA addrs.
func (d *SpanDir) ResolvedAddrsAAAA() ([]string, error) {
	return d.resolvedAddrs("sondaDnsRecordsAAAA")
}

func (d *SpanDir) resolvedAddrs(msgName string) ([]string, error) {
	filep, err := d.Env.OpenFile(ptnoppaths.SpanStdout(d.Path), os.O_RDONLY, 0)
	if err != nil {
		return nil, ReadError{err}
	}
	defer filep.Close()

	var addrs []string
	scanner := bufio.NewScanner(filep)
	scanner.Buffer(nil, maxLineSize)
	for scanner.Scan() {
		ev, err := ptnopdata.ParseEvent(scanner.Bytes())
		if err != nil {
			return nil, ReadError{err}
		}
		if ev.Msg != msgName {
			continue
		}
		addrs = append(addrs, ev.DNSRecordsList...)
	}

	if err := scanner.Err(); err != nil {
		return nil, ReadError{err}
	}

	if len(addrs) <= 0 {
		return nil, ErrNoData
	}
	return addrs, nil
}

// ReflexiveAddr returns the STUN reflexive address.
//
// Failure modes:
//
//  1. [ErrNoData] if we cannot find the matching structured logs
//
//  2. [ReadError] if we cannot open, scan, or parse the structured logs file
func (d *SpanDir) ReflexiveAddr() (string, error) {
	filep, err := d.Env.OpenFile(ptnoppaths.SpanStdout(d.Path), os.O_RDONLY, 0)
	if err != nil {
		return "", ReadError{err}
	}
	defer filep.Close()

	scanner := bufio.NewScanner(filep)
	scanner.Buffer(nil, maxLineSize)
	for scanner.Scan() {
		ev, err := ptnopdata.ParseEvent(scanner.Bytes())
		if err != nil {
			return "", ReadError{err}
		}
		if ev.Msg != "stunBindingResult" {
			continue
		}
		if ev.STUNReflexiveAddr == "" {
			continue
		}
		return ev.STUNReflexiveAddr, nil
	}

	if err := scanner.Err(); err != nil {
		return "", ReadError{err}
	}
	return "", ErrNoData
}
