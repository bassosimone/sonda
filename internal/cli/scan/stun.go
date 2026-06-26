// SPDX-License-Identifier: GPL-3.0-or-later

package scan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"

	"github.com/bassosimone/sonda/internal/netstack"
)

// stunRunner performs STUN lookups and writes reflexive addresses
// as tags into the shared state.
type stunRunner struct {
	Logger   *slog.Logger
	Measurer *netstack.SondaMeasurer
	Resolver *netstack.Resolver
	State    *sharedState
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
	addrs, err := r.Resolver.LookupHost(ctx, server)
	if err != nil {
		return fmt.Errorf("stun: resolving %s: %w", server, err)
	}

	// Perform STUN lookups against each resolved address.
	reflexives, err := stunLookup(ctx, r.Measurer, addrs, port)
	if err != nil {
		return fmt.Errorf("stun: %w", err)
	}

	// Write reflexive addresses into the shared state.
	for _, addr := range reflexives {
		parsed, err := netip.ParseAddr(addr)
		if err != nil {
			r.Logger.Warn("STUN returned unparseable address", slog.String("addr", addr))
			continue
		}
		if parsed.Is4() {
			r.State.SetTag("reflexiveAddrV4", addr)
		} else {
			r.State.SetTag("reflexiveAddrV6", addr)
		}
	}
	return nil
}

// stunLookup performs STUN lookups against all the given addresses and
// returns the reflexive addresses found. Fails only if all addresses fail.
func stunLookup(ctx context.Context, measurer *netstack.SondaMeasurer, addrs []string, port string) ([]string, error) {
	var (
		errs       []error
		reflexives []string
	)
	for _, addr := range addrs {
		stun := netstack.NewSTUNLookupper(measurer)
		stun.ServerAddr = net.JoinHostPort(addr, port)
		reflexive, err := stun.LookupIPAddr(ctx)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		reflexives = append(reflexives, reflexive)
	}
	if len(reflexives) <= 0 {
		return nil, errors.Join(errs...)
	}
	return reflexives, nil
}
