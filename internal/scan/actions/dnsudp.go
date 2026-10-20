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

// dnsOverUDPAction is the action that measures dns-over-udp endpoints.
type dnsOverUDPAction struct {
	Conn   WorkflowPtnopConn
	Runner *WorkflowRunner
}

// newDNSOverUDPAction constructs a new [*dnsOverUDPAction] instance.
func newDNSOverUDPAction(conn WorkflowPtnopConn, runner *WorkflowRunner) WorkflowAction {
	return &dnsOverUDPAction{
		Conn:   conn,
		Runner: runner,
	}
}

// DNSOverUDPWith contains the "with" block parsed by dns-over-udp.
type DNSOverUDPWith struct {
	Optimize  string `yaml:"optimize"`
	Port      uint16 `yaml:"port"`
	QueryName string `yaml:"query_name"`
	Server    string `yaml:"server"`
}

var _ WorkflowAction = &dnsOverUDPAction{}

// Run implements [WorkflowAction].
func (a *dnsOverUDPAction) Run(ctx context.Context, logger *slog.Logger, with ast.Node) error {
	// 1. Parse arguments passed to the step.
	runtimex.Assert(with != nil)
	var withData DNSOverUDPWith
	if err := yaml.NodeToValue(with, &withData); err != nil {
		return err
	}
	if withData.Server == "" {
		return fmt.Errorf("dns-over-udp: missing 'server' parameter")
	}
	if withData.Port == 0 {
		withData.Port = 53
	}
	if withData.QueryName == "" {
		return fmt.Errorf("dns-over-udp: missing 'query_name' parameter")
	}

	// 2. Resolve the server hostname to addresses.
	addrs, err := a.Runner.lookupHost(ctx, a.Conn, withData.Server, withData.Optimize)
	if err != nil {
		return fmt.Errorf("dns-over-udp: resolving %s: %w", withData.Server, err)
	}

	// 3. Perform a DNS-over-UDP lookup against each resolved address.
	for _, addr := range addrs {
		req := &ptnoprpc.Request{
			AddrPort:     net.JoinHostPort(addr, strconv.Itoa(int(withData.Port))), // uint16 -> int
			DNSQueryName: withData.QueryName,
			DNSQueryType: "A",
			Optimize:     withData.Optimize,
			Pipeline:     "dns-over-udp",
			Tags:         a.Runner.tags(),
			Timeout:      5 * time.Second,
		}
		if _, err := a.Runner.runPtnop(ctx, a.Conn, req); err != nil {
			return fmt.Errorf("dns-over-udp: ptnop: %w", err)
		}
	}
	return nil
}
