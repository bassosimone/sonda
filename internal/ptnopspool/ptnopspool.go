// SPDX-License-Identifier: GPL-3.0-or-later

// Package ptnopspool contains code to interact with the ptnop spool.
package ptnopspool

import (
	"bufio"
	"errors"
	"fmt"

	"github.com/bassosimone/sonda/internal/ptnopdata"
	"github.com/bassosimone/sonda/internal/spool"
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

// DataTypeError is an error indicating that a [spool.SpanDir] has the wrong data type.
type DataTypeError struct {
	Got string
}

// Error returns a string representation of the error.
func (e DataTypeError) Error() string {
	return fmt.Sprintf("ptnopspool: expected dataType to be %q, got %q", "ptnop", e.Got)
}

// ResolvedAddrsA returns the resolved IPv4 addrs.
//
// Failure modes:
//
//  1. [ErrNoData] if we cannot find the matching structured logs
//
//  2. [ReadError] if we cannot open, scan, or parse the structured logs file
//
//  3. [DataTypeError] if the spanDir dataType is not "ptnop".
func ResolvedAddrsA(spanDir spool.SpanDir) ([]string, error) {
	return resolvedAddrs(spanDir, "sondaDnsRecordsA")
}

// ResolvedAddrsAAAA is like [ResolvedAddrsA] but for AAAA addrs.
func ResolvedAddrsAAAA(spanDir spool.SpanDir) ([]string, error) {
	return resolvedAddrs(spanDir, "sondaDnsRecordsAAAA")
}

func resolvedAddrs(spanDir spool.SpanDir, msgName string) ([]string, error) {
	if value := spanDir.DataType(); value != "ptnop" {
		return nil, DataTypeError{value}
	}

	filep, err := spanDir.OpenEntity("stdout.txt")
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
//
//  3. [DataTypeError] if the spanDir dataType is not "ptnop".
func ReflexiveAddr(spanDir spool.SpanDir) (string, error) {
	if value := spanDir.DataType(); value != "ptnop" {
		return "", DataTypeError{value}
	}

	filep, err := spanDir.OpenEntity("stdout.txt")
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
