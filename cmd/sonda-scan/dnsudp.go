// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/bassosimone/sonda/internal/ptnoprpc"
)

// dnsOverUDPRunner runs a DNS-over-UDP lookup.
type dnsOverUDPRunner struct {
	Client *ptnoprpc.Client
	State  *sharedState
}

// RunStep implements StepRunner.
func (r *dnsOverUDPRunner) RunStep(ctx context.Context, with map[string]string) error {
	// Parse arguments passed to the step.
	server := with["server"]
	if server == "" {
		return fmt.Errorf("dns-over-udp: missing 'server' parameter")
	}
	port := with["port"]
	if port == "" {
		port = "53"
	}
	query := with["query"]
	if query == "" {
		return fmt.Errorf("dns-over-udp: missing 'query' parameter")
	}

	// Resolve the server hostname to addresses.
	addrs, err := lookupHost(ctx, r.Client, r.State, server)
	if err != nil {
		return fmt.Errorf("dns-over-udp: resolving %s: %w", server, err)
	}

	// Perform a DNS-over-UDP lookup against each resolved address.
	for _, addr := range addrs {
		req := &ptnoprpc.Request{
			AddrPort:     net.JoinHostPort(addr, port),
			DNSQueryName: query,
			DNSQueryType: "A",
			Pipeline:     "dns-over-udp",
			Tags:         r.State.Tags(),
			Timeout:      5 * time.Second,
		}
		if _, err := r.State.RunAndSave(ctx, r.Client, req); err != nil {
			return fmt.Errorf("dns-over-udp: %w", err)
		}
	}
	return nil
}
