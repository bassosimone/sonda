// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/bassosimone/sonda/internal/ptnopclient"
	"github.com/bassosimone/sonda/internal/ptnoprpc"
)

// httpsRunner runs an HTTPS GET measurement against each resolved address.
type httpsRunner struct {
	Conn  *ptnopclient.Conn
	State *sharedState
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
	var headers []string
	if value := with["user_agent"]; value != "" {
		headers = append(headers, "User-Agent: "+value)
	}

	// Resolve the host to addresses.
	addrs, err := lookupHost(ctx, r.Conn, r.State, with, host)
	if err != nil {
		return fmt.Errorf("https: resolving %s: %w", host, err)
	}

	// Perform an HTTPS GET against each resolved address.
	for _, addr := range addrs {
		req := &ptnoprpc.Request{
			ALPN:         []string{"h2", "http/1.1"},
			AddrPort:     net.JoinHostPort(addr, port),
			HTTPBodyFile: false,
			HTTPHeaders:  headers,
			HTTPHost:     host,
			HTTPMethod:   "GET",
			HTTPScheme:   "https",
			Optimize:     with["optimize"],
			Pipeline:     "https",
			SNI:          host,
			Tags:         r.State.Tags(),
			Timeout:      30 * time.Second,
			URLPath:      urlPath,
		}
		if _, err := r.State.RunAndSave(ctx, r.Conn, req); err != nil {
			return fmt.Errorf("https: %w", err)
		}
	}
	return nil
}
