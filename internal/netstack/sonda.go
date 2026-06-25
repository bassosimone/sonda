// SPDX-License-Identifier: GPL-3.0-or-later

package netstack

import (
	"context"
	"os/exec"
	"time"

	"github.com/bassosimone/nop"
	"github.com/bassosimone/sonda/internal/paths"
	"github.com/bassosimone/sonda/internal/testable"
)

// SondaOperation describes a measurement operation's command-line arguments.
type SondaOperation interface {
	Args() []string
}

// SondaMeasurer runs measurement operations through the spool directory.
//
// Use [NewSondaMeasurer] to construct.
type SondaMeasurer struct {
	// Env is the environment for running commands.
	//
	// Set by [NewSondaMeasurer].
	Env *testable.Environ

	// Executable is the path to the sonda binary.
	//
	// Optional; default: the result of [testable.Environ.Executable].
	Executable string

	// SpoolDir is the spool directory path.
	//
	// Set by [NewSondaMeasurer].
	SpoolDir string
}

// NewSondaMeasurer creates a [*SondaMeasurer] with the given spool directory.
func NewSondaMeasurer(env *testable.Environ, spoolDir string) *SondaMeasurer {
	return &SondaMeasurer{Env: env, SpoolDir: spoolDir}
}

// Run executes an operation through `sonda spool run` and returns
// the span directory containing the measurement output.
func (s *SondaMeasurer) Run(ctx context.Context, op SondaOperation) (string, error) {
	// Honor the executable override when present.
	exe := s.Executable
	if exe == "" {
		var err error
		exe, err = s.Env.Executable()
		if err != nil {
			return "", err
		}
	}

	// Create the command to run with externally defined spanID so that
	// later on we can read the `stdout.txt`.
	spanID := nop.NewSpanID()
	args := []string{"spool", "run", "--span-id", spanID, "--spool-dir", s.SpoolDir, "--"}
	args = append(args, exe)
	args = append(args, op.Args()...)
	cmd := exec.CommandContext(ctx, exe, args...)

	// Execute the command.
	if err := s.Env.RunCommand(cmd); err != nil {
		return "", err
	}

	// On success, return the path to the spool dir.
	return paths.SpanDir(s.SpoolDir, spanID), nil
}

// SondaMeasureSTUN is the operation for `sonda measure stun`.
type SondaMeasureSTUN struct {
	// Target is the STUN server address and port.
	//
	// Optional; default: "74.125.250.129:19302".
	Target string

	// Timeout is the measurement timeout.
	//
	// Optional; default: 30s.
	Timeout time.Duration
}

// Args implements [SondaOperation].
func (op *SondaMeasureSTUN) Args() []string {
	args := []string{"measure", "stun"}

	if op.Target != "" {
		args = append(args, "--target", op.Target)
	}

	if op.Timeout != 0 {
		args = append(args, "--timeout", op.Timeout.String())
	}

	return args
}

// SondaMeasureDNSOverUDP is the operation for `sonda measure dns over udp`.
type SondaMeasureDNSOverUDP struct {
	// Domain is the domain name to resolve.
	//
	// Mandatory.
	Domain string

	// QueryType is the DNS query type (e.g., "A", "AAAA").
	//
	// Mandatory.
	QueryType string

	// Target is the DNS server address and port.
	//
	// Optional; default: "8.8.8.8:53".
	Target string

	// Timeout is the measurement timeout.
	//
	// Optional; default: 30s.
	Timeout time.Duration
}

// Args implements [SondaOperation].
func (op *SondaMeasureDNSOverUDP) Args() []string {
	args := []string{"measure", "dns", "over", "udp"}

	if op.Domain != "" {
		args = append(args, "--domain", op.Domain)
	}

	if op.QueryType != "" {
		args = append(args, "--query-type", op.QueryType)
	}

	if op.Target != "" {
		args = append(args, "--target", op.Target)
	}

	if op.Timeout != 0 {
		args = append(args, "--timeout", op.Timeout.String())
	}

	return args
}

// SondaMeasureDNSOverHTTPS is the operation for `sonda measure dns over https`.
type SondaMeasureDNSOverHTTPS struct {
	// Domain is the domain name to resolve.
	//
	// Mandatory.
	Domain string

	// HTTPHost is the HTTP Host header value.
	//
	// Optional; default: "dns.google".
	HTTPHost string

	// QueryType is the DNS query type (e.g., "A", "AAAA").
	//
	// Mandatory.
	QueryType string

	// SNI is the TLS Server Name Indication.
	//
	// Optional; default: "dns.google".
	SNI string

	// Target is the DNS server address and port.
	//
	// Optional; default: "8.8.8.8:443".
	Target string

	// Timeout is the measurement timeout.
	//
	// Optional; default: 30s.
	Timeout time.Duration

	// URLPath is the DoH URL path.
	//
	// Optional; default: "/dns-query".
	URLPath string
}

// Args implements [SondaOperation].
func (op *SondaMeasureDNSOverHTTPS) Args() []string {
	args := []string{"measure", "dns", "over", "https"}

	if op.Domain != "" {
		args = append(args, "--domain", op.Domain)
	}

	if op.HTTPHost != "" {
		args = append(args, "--http-host", op.HTTPHost)
	}

	if op.QueryType != "" {
		args = append(args, "--query-type", op.QueryType)
	}

	if op.SNI != "" {
		args = append(args, "--sni", op.SNI)
	}

	if op.Target != "" {
		args = append(args, "--target", op.Target)
	}

	if op.Timeout != 0 {
		args = append(args, "--timeout", op.Timeout.String())
	}

	if op.URLPath != "" {
		args = append(args, "--url-path", op.URLPath)
	}

	return args
}
