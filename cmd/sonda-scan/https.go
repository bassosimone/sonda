// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/bassosimone/sonda/internal/ptnoprpc"
)

// httpsRunner runs an HTTPS GET measurement against each resolved address.
type httpsRunner struct {
	Client *ptnoprpc.Client
	State  *sharedState
}

// RunStep implements StepRunner.
func (r *httpsRunner) RunStep(ctx context.Context, with map[string]string) error {
	// Parse arguments passed to the step.
	host := with["host"]
	if host == "" {
		return fmt.Errorf("https: missing 'host' parameter")
	}
	port := with["port"]
	if port == "" {
		port = "443"
	}
	urlPath := with["url_path"]
	if urlPath == "" {
		urlPath = "/"
	}

	// Resolve the host to addresses.
	addrs, err := lookupHost(ctx, r.Client, r.State, host)
	if err != nil {
		return fmt.Errorf("https: resolving %s: %w", host, err)
	}

	// Perform an HTTPS GET against each resolved address.
	for _, addr := range addrs {
		req := &ptnoprpc.Request{
			ALPN:         []string{"h2", "http/1.1"},
			AddrPort:     net.JoinHostPort(addr, port),
			HTTPBodyFile: false,
			HTTPHost:     host,
			HTTPMethod:   "GET",
			HTTPScheme:   "https",
			Pipeline:     "https",
			SNI:          host,
			Tags:         r.State.Tags(),
			Timeout:      30 * time.Second,
			URLPath:      urlPath,
		}
		if _, err := r.State.RunAndSave(ctx, r.Client, req); err != nil {
			return fmt.Errorf("https: %w", err)
		}
	}
	return nil
}
