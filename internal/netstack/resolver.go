// SPDX-License-Identifier: GPL-3.0-or-later

package netstack

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/paths"
	"github.com/bassosimone/sonda/internal/structured"
)

// DNSTransport is the interface for DNS lookup transports.
type DNSTransport interface {
	LookupA(ctx context.Context, domain string) ([]string, error)
	LookupAAAA(ctx context.Context, domain string) ([]string, error)
}

// Resolver performs DNS lookups using a [DNSTransport].
//
// Use [NewResolver] to construct.
type Resolver struct {
	// Transport is the underlying DNS transport.
	//
	// Set by [NewResolver].
	Transport DNSTransport
}

// NewResolver creates a [*Resolver] with the given transport.
func NewResolver(transport DNSTransport) *Resolver {
	return &Resolver{Transport: transport}
}

// LookupHost resolves a domain name and returns both IPv4 and IPv6 addresses.
func (rx *Resolver) LookupHost(ctx context.Context, domain string) ([]string, error) {
	// Short-circuit if the input is already an IP address.
	if addr, err := netip.ParseAddr(domain); err == nil {
		return []string{addr.String()}, nil
	}

	// We run a sequential lookup since this is a batch tool.
	addrsA, errA := rx.Transport.LookupA(ctx, domain)
	addrsAAAA, errAAAA := rx.Transport.LookupAAAA(ctx, domain)

	// Merge the addrs and determine whether we succeeded.
	addrs := append(addrsA, addrsAAAA...)
	if len(addrs) <= 0 {
		err := errors.Join(errA, errAAAA)
		runtimex.Assert(err != nil)
		return nil, err
	}
	return addrs, nil
}

// LookupA resolves a domain name and returns the IPv4 addresses.
func (rx *Resolver) LookupA(ctx context.Context, domain string) ([]string, error) {
	return rx.Transport.LookupA(ctx, domain)
}

// LookupAAAA resolves a domain name and returns the IPv6 addresses.
func (rx *Resolver) LookupAAAA(ctx context.Context, domain string) ([]string, error) {
	return rx.Transport.LookupAAAA(ctx, domain)
}

// readResponseAddrs reads stdout.txt from a span directory and extracts
// addresses from the structured log event identified by msgName.
func readResponseAddrs(spanDir, msgName string) ([]string, error) {
	filep, err := os.Open(paths.SpanStdout(spanDir))
	if err != nil {
		return nil, err
	}
	defer filep.Close()

	var addrs []string
	scanner := bufio.NewScanner(filep)
	for scanner.Scan() {
		ev, err := structured.ParseEvent(scanner.Bytes())
		if err != nil {
			continue
		}
		if ev.Msg != msgName {
			continue
		}
		addrs = append(addrs, ev.DNSRecordsList...)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if len(addrs) <= 0 {
		return nil, fmt.Errorf("no %s records found", msgName)
	}
	return addrs, nil
}
