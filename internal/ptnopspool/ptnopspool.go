// SPDX-License-Identifier: GPL-3.0-or-later

// Package ptnopspool contains code to interact with the ptnop spool.
package ptnopspool

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/bassosimone/sonda/internal/paths"
	"github.com/bassosimone/sonda/internal/ptnopdata"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/google/uuid"
)

// newSpanID returns a new spanID.
func newSpanID() string {
	return uuid.Must(uuid.NewV7()).String()
}

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

// ExecError is an error indicating that we cannot execute the ptnop plugin.
type ExecError struct {
	Err error
}

// Error returns a string representation of the error.
func (e ExecError) Error() string {
	return e.Err.Error()
}

// Unwrap returns the underlying error.
func (e ExecError) Unwrap() error {
	return e.Err
}

// Options contains command line options for the ptnop plugin.
//
// You should fill the options relevant for the pipeline you want
// to run and you can safely leave the others empty.
type Options struct {
	ALPN         []string
	AddrPort     string
	DNSQueryName string
	DNSQueryType string
	HTTPBodyFile string
	HTTPHeaders  []string
	HTTPHost     string
	HTTPMethod   string
	HTTPScheme   string
	Pipeline     string
	SNI          string
	Tags         []string
	Timeout      time.Duration
	URLPath      string
}

// RootDir is the root spool dir assigned to the ptnop plugin.
//
// Use [NewRootDir] to construct.
type RootDir struct {
	Env  *testable.Environ
	Path string
}

// NewRootDir creates and returns a new [*RootDir] instance.
func NewRootDir(env *testable.Environ, spoolDir string) *RootDir {
	return &RootDir{Env: env, Path: spoolDir}
}

// Run runs a measurement pipeline and returns its [*SpanDir].
//
// This command fails with [ExecError] for any child status code different from `0` (success)
// and `1` (measurement failure), thus covering, among other things, missing plugins.
func (d *RootDir) Run(ctx context.Context, opts *Options) (*SpanDir, error) {
	// Create the command to run with externally defined spanID so that
	// later on we can read the `stdout.txt`.
	//
	// Passing all possible command line options, including empty lines, is
	// fine because the plugin handles this gracefully.
	spanID := newSpanID()
	args := []string{
		"spool", "run",
		"--span-id", spanID,
		"--spool-dir", d.Path,
		"--",
		"measure-ptnop",
		"--addr-port", opts.AddrPort,
		"--dns-query-name", opts.DNSQueryName,
		"--dns-query-type", opts.DNSQueryType,
		"--http-body-file", opts.HTTPBodyFile,
		"--http-host", opts.HTTPHost,
		"--http-method", opts.HTTPMethod,
		"--http-scheme", opts.HTTPScheme,
		"--pipeline", opts.Pipeline,
		"--sni", opts.SNI,
		"--url-path", opts.URLPath,
	}
	for _, e := range opts.ALPN {
		args = append(args, "--alpn")
		args = append(args, e)
	}
	for _, e := range opts.HTTPHeaders {
		args = append(args, "--http-header")
		args = append(args, e)
	}
	for _, e := range opts.Tags {
		args = append(args, "--tag")
		args = append(args, e)
	}
	if opts.Timeout > 0 {
		args = append(args, "--timeout")
		args = append(args, opts.Timeout.String())
	}

	// Execute the command.
	if err := d.Env.ReExec(ctx, args); err != nil {
		return nil, ExecError{err}
	}

	// Read the exitcode and handle exit codes different from `0` (success)
	// and `1` (failure). By doing this, we cover `127` (subcommand execution
	// failed), and `2` (usage error including unknown subcommand), as
	// well as all the other unexpected exit codes.
	spanDir := paths.SpanDir(d.Path, spanID)
	data, err := d.Env.ReadFile(paths.SpanExitCode(spanDir))
	if err != nil {
		return nil, ExecError{err}
	}
	exitCode, err := strconv.Atoi(string(bytes.TrimSpace(data)))
	if err != nil {
		return nil, ExecError{err}
	}
	if exitCode != 0 && exitCode != 1 {
		err := fmt.Errorf("unexpected exit code %d (see %s/stderr.txt)", exitCode, spanDir)
		return nil, ExecError{err}
	}

	// On success, return the span directory.
	sdx := &SpanDir{
		Env:      d.Env,
		ExitCode: exitCode,
		Path:     spanDir,
	}
	return sdx, nil
}

// SpanDir is the directory containing the result of running a pipeline.
//
// Returned by [*RootDir.Run].
//
// The Env is same as in the [*RootDir].
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
	filep, err := d.Env.OpenFile(paths.SpanStdout(d.Path), os.O_RDONLY, 0)
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
	filep, err := d.Env.OpenFile(paths.SpanStdout(d.Path), os.O_RDONLY, 0)
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
