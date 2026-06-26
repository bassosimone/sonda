// SPDX-License-Identifier: GPL-3.0-or-later

package scan

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/bassosimone/sonda/internal/netstack"
)

// dnsOverHTTPSRunner runs a DNS-over-HTTPS lookup.
type dnsOverHTTPSRunner struct {
	Logger   *slog.Logger
	Measurer *netstack.SondaMeasurer
	Resolver *netstack.Resolver
	State    *sharedState
}

// RunStep implements StepRunner.
func (r *dnsOverHTTPSRunner) RunStep(ctx context.Context, with map[string]string) error {
	// Inject tags from the shared state into the context.
	if tags := r.State.Tags(); len(tags) > 0 {
		ctx = netstack.ContextWithTags(ctx, tags)
	}

	// Parse arguments passed to the step.
	server := with["server"]
	if server == "" {
		return fmt.Errorf("dns-over-https: missing 'server' parameter")
	}
	port := with["port"]
	if port == "" {
		port = "443"
	}
	query := with["query"]
	if query == "" {
		return fmt.Errorf("dns-over-https: missing 'query' parameter")
	}

	// Resolve the server hostname to addresses.
	addrs, err := r.Resolver.LookupHost(ctx, server)
	if err != nil {
		return fmt.Errorf("dns-over-https: resolving %s: %w", server, err)
	}

	// Perform a DNS-over-HTTPS lookup against each resolved address.
	for _, addr := range addrs {
		txp := netstack.NewDNSOverHTTPSTransport(r.Measurer)
		txp.ServerAddr = net.JoinHostPort(addr, port)
		resolver := netstack.NewResolver(txp)
		if _, err := resolver.LookupHost(ctx, query); err != nil {
			r.Logger.Warn("DNS over HTTPS failed", slog.String("addr", addr), slog.Any("err", err))
		}
	}
	return nil
}
