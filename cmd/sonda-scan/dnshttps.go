// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/bassosimone/sonda/internal/ptnopspool"
)

// dnsOverHTTPSRunner runs a DNS-over-HTTPS lookup.
type dnsOverHTTPSRunner struct {
	RootDir *ptnopspool.RootDir
	State   *sharedState
}

// RunStep implements StepRunner.
func (r *dnsOverHTTPSRunner) RunStep(ctx context.Context, with map[string]string) error {
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
	addrs, err := lookupHost(ctx, r.RootDir, r.State, server)
	if err != nil {
		return fmt.Errorf("dns-over-https: resolving %s: %w", server, err)
	}

	// Perform a DNS-over-HTTPS lookup against each resolved address.
	for _, addr := range addrs {
		opts := &ptnopspool.Options{
			ALPN:         []string{"h2", "http/1.1"},
			AddrPort:     net.JoinHostPort(addr, port),
			DNSQueryName: query,
			DNSQueryType: "A",
			HTTPHost:     server,
			HTTPMethod:   "POST",
			HTTPScheme:   "https",
			Pipeline:     "dns-over-https",
			SNI:          server,
			Tags:         r.State.Tags(),
			Timeout:      5 * time.Second,
			URLPath:      "/dns-query",
		}
		if _, err := r.RootDir.Run(ctx, opts); err != nil {
			return fmt.Errorf("dns-over-https: %w", err)
		}
	}
	return nil
}
