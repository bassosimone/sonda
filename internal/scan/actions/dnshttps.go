// SPDX-License-Identifier: GPL-3.0-or-later

package actions

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/ptnoprpc"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
)

// dnsOverHTTPSAction is the action that measures dns-over-https endpoints.
type dnsOverHTTPSAction struct {
	Conn   WorkflowPtnopConn
	Runner *WorkflowRunner
}

// newDNSOverHTTPSAction constructs a new [*dnsOverHTTPSAction] instance.
func newDNSOverHTTPSAction(conn WorkflowPtnopConn, runner *WorkflowRunner) WorkflowAction {
	return &dnsOverHTTPSAction{
		Conn:   conn,
		Runner: runner,
	}
}

// DNSOverHTTPSWith contains the "with" block parsed by dns-over-https.
type DNSOverHTTPSWith struct {
	Optimize  string `yaml:"optimize"`
	Port      uint16 `yaml:"port"`
	QueryName string `yaml:"query_name"`
	Server    string `yaml:"server"`
	UserAgent string `yaml:"user_agent"`
}

var _ WorkflowAction = &dnsOverHTTPSAction{}

// Run implements [WorkflowAction].
func (a *dnsOverHTTPSAction) Run(ctx context.Context, logger *slog.Logger, with ast.Node) error {
	// 1. Parse arguments passed to the step.
	runtimex.Assert(with != nil)
	var withData DNSOverHTTPSWith
	if err := yaml.NodeToValue(with, &withData); err != nil {
		return err
	}
	if withData.Server == "" {
		return fmt.Errorf("dns-over-https: missing 'server' parameter")
	}
	if withData.Port == 0 {
		withData.Port = 443
	}
	if withData.QueryName == "" {
		return fmt.Errorf("dns-over-https: missing 'query_name' parameter")
	}
	var headers []string
	if value := withData.UserAgent; value != "" {
		headers = append(headers, "User-Agent: "+value)
	}

	// 2. Resolve the server hostname to addresses.
	addrs, err := a.Runner.lookupHost(ctx, a.Conn, withData.Server, withData.Optimize)
	if err != nil {
		return fmt.Errorf("dns-over-https: resolving %s: %w", withData.Server, err)
	}

	// 3. Perform a DNS-over-HTTPS lookup against each resolved address.
	for _, addr := range addrs {
		req := &ptnoprpc.Request{
			ALPN:         []string{"h2", "http/1.1"},
			AddrPort:     net.JoinHostPort(addr, strconv.Itoa(int(withData.Port))), // uint16 -> int
			DNSQueryName: withData.QueryName,
			DNSQueryType: "A",
			HTTPHeaders:  headers,
			HTTPHost:     withData.Server,
			HTTPMethod:   "POST",
			HTTPScheme:   "https",
			Optimize:     withData.Optimize,
			Pipeline:     "dns-over-https",
			SNI:          withData.Server,
			Tags:         a.Runner.tags(),
			Timeout:      5 * time.Second,
			URLPath:      "/dns-query",
		}
		if _, err := a.Runner.runPtnop(ctx, a.Conn, req); err != nil {
			return fmt.Errorf("dns-over-https: ptnop: %w", err)
		}
	}
	return nil
}
