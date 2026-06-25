// SPDX-License-Identifier: GPL-3.0-or-later

package scan

import (
	"context"
	"errors"
	"net"

	"github.com/bassosimone/sonda/internal/netstack"
)

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
