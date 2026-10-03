// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/bassosimone/sonda/internal/ptnoprpc"
	"github.com/bassosimone/sonda/internal/ptnopspool"
)

// stunRunner performs STUN lookups and writes reflexive addresses
// as tags into the shared state.
type stunRunner struct {
	Client *ptnoprpc.Client
	State  *sharedState
}

// RunStep implements stepRunner.
func (r *stunRunner) RunStep(ctx context.Context, with map[string]string) error {
	// Parse arguments passed to the step.
	server := with["server"]
	if server == "" {
		return fmt.Errorf("stun: missing 'server' parameter")
	}
	port := with["port"]
	if port == "" {
		port = "19302"
	}

	// Resolve the server hostname to addresses.
	addrs, err := lookupHost(ctx, r.Client, r.State, server)
	if err != nil {
		return fmt.Errorf("stun: resolving %s: %w", server, err)
	}

	// Perform STUN lookups against each resolved address.
	var reflexives []string
	for _, addr := range addrs {
		req := &ptnoprpc.Request{
			AddrPort: net.JoinHostPort(addr, port),
			Pipeline: "stun",
			Tags:     r.State.Tags(),
			Timeout:  5 * time.Second,
		}
		spanDir, err := r.Client.Run(ctx, req)
		if err != nil {
			return fmt.Errorf("stun: %w", err)
		}
		refaddr, err := spanDir.ReflexiveAddr()
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

	// Write reflexive addresses into the shared state.
	for _, addr := range reflexives {
		parsed, err := netip.ParseAddr(addr)
		if err != nil {
			return fmt.Errorf("stun: %w", err)
		}
		if parsed.Is4() {
			r.State.SetTag("reflexiveAddrV4", addr)
		} else {
			r.State.SetTag("reflexiveAddrV6", addr)
		}
	}
	return nil
}
