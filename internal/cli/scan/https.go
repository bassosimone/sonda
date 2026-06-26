// SPDX-License-Identifier: GPL-3.0-or-later

package scan

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/bassosimone/sonda/internal/netstack"
)

// httpsRunner runs an HTTPS GET measurement against each resolved address.
type httpsRunner struct {
	Logger   *slog.Logger
	Measurer *netstack.SondaMeasurer
	Resolver *netstack.Resolver
	State    *sharedState
}

// RunStep implements StepRunner.
func (r *httpsRunner) RunStep(ctx context.Context, with map[string]string) error {
	// Inject tags from the shared state into the context.
	if tags := r.State.Tags(); len(tags) > 0 {
		ctx = netstack.ContextWithTags(ctx, tags)
	}

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
	addrs, err := r.Resolver.LookupHost(ctx, host)
	if err != nil {
		return fmt.Errorf("https: resolving %s: %w", host, err)
	}

	// Perform an HTTPS GET against each resolved address.
	for _, addr := range addrs {
		op := &netstack.SondaMeasureHTTPS{
			HTTPHost: host,
			SNI:      host,
			Target:   net.JoinHostPort(addr, port),
			URLPath:  urlPath,
		}
		if _, err := r.Measurer.Run(ctx, op); err != nil {
			r.Logger.Warn("HTTPS GET failed", slog.String("addr", addr), slog.Any("err", err))
		}
	}
	return nil
}
