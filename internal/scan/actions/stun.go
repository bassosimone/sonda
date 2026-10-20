// SPDX-License-Identifier: GPL-3.0-or-later

package actions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/ptnoprpc"
	"github.com/bassosimone/sonda/internal/ptnopspool"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
)

// stunAction is the action that measures stun endpoints.
//
// Construct using [newSTUNAction].
type stunAction struct {
	Conn   WorkflowPtnopConn
	Runner *WorkflowRunner
}

// newSTUNAction constructs a new [*stunAction] instance.
func newSTUNAction(conn WorkflowPtnopConn, runner *WorkflowRunner) WorkflowAction {
	return &stunAction{
		Conn:   conn,
		Runner: runner,
	}
}

// STUNWith contains the "with" block parsed by stun.
type STUNWith struct {
	Optimize string `yaml:"optimize"`
	Port     uint16 `yaml:"port"`
	Server   string `yaml:"server"`
}

var _ WorkflowAction = &stunAction{}

// Run implements [WorkflowAction].
func (a *stunAction) Run(ctx context.Context, logger *slog.Logger, with ast.Node) error {
	// 1. Parse arguments passed to the step.
	runtimex.Assert(with != nil)
	var withData STUNWith
	if err := yaml.NodeToValue(with, &withData); err != nil {
		return err
	}
	if withData.Server == "" {
		return fmt.Errorf("stun: missing 'server' parameter")
	}
	if withData.Port == 0 {
		withData.Port = 3478
	}

	// 2. Resolve the server hostname to addresses.
	addrs, err := a.Runner.lookupHost(ctx, a.Conn, withData.Server, withData.Optimize)
	if err != nil {
		return fmt.Errorf("stun: resolving %s: %w", withData.Server, err)
	}

	// 3. Perform STUN lookups against each resolved address.
	var reflexives []string
	for _, addr := range addrs {
		req := &ptnoprpc.Request{
			AddrPort: net.JoinHostPort(addr, strconv.Itoa(int(withData.Port))), // uint16 -> int
			Optimize: withData.Optimize,
			Pipeline: "stun",
			Tags:     a.Runner.tags(),
			Timeout:  5 * time.Second,
		}
		spanDir, err := a.Runner.runPtnop(ctx, a.Conn, req)
		if err != nil {
			return fmt.Errorf("stun: ptnop: %w", err)
		}
		refaddr, err := ptnopspool.ReflexiveAddr(spanDir)
		if errors.Is(err, ptnopspool.ErrNoData) {
			continue // typical case: we have no IPv6 support
		}
		if err != nil {
			return fmt.Errorf("stun: %w", err)
		}
		reflexives = append(reflexives, refaddr)
	}
	if len(reflexives) <= 0 {
		return errors.New("stun: no address found")
	}

	// 4. Write reflexive addresses into the shared state.
	v6Sup := false
	for _, addr := range reflexives {
		parsed, err := netip.ParseAddr(addr)
		if err != nil {
			return fmt.Errorf("stun: %w", err)
		}
		if parsed.Is4() {
			a.Runner.setTag("reflexiveAddrV4", addr)
		} else {
			a.Runner.setTag("reflexiveAddrV6", addr)
			v6Sup = true
		}
	}
	a.Runner.setSupportsV6(v6Sup)
	return nil
}
