// SPDX-License-Identifier: GPL-3.0-or-later

package scan

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/bassosimone/sonda/internal/netstack"
)

// dnsOverUDPRunner runs a DNS-over-UDP lookup.
type dnsOverUDPRunner struct {
	Logger   *slog.Logger
	Measurer *netstack.SondaMeasurer
	Resolver *netstack.Resolver
	State    *sharedState
}

// RunStep implements StepRunner.
func (r *dnsOverUDPRunner) RunStep(ctx context.Context, with map[string]string) error {
	// Inject tags from the shared state into the context.
	if tags := r.State.Tags(); len(tags) > 0 {
		ctx = netstack.ContextWithTags(ctx, tags)
	}

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
	addrs, err := r.Resolver.LookupHost(ctx, server)
	if err != nil {
		return fmt.Errorf("dns-over-udp: resolving %s: %w", server, err)
	}

	// Perform a DNS-over-UDP lookup against each resolved address.
	for _, addr := range addrs {
		txp := netstack.NewDNSOverUDPTransport(r.Measurer)
		txp.ServerAddr = net.JoinHostPort(addr, port)
		resolver := netstack.NewResolver(txp)
		if _, err := resolver.LookupHost(ctx, query); err != nil {
			r.Logger.Warn("DNS over UDP failed", slog.String("addr", addr), slog.Any("err", err))
		}
	}
	return nil
}
