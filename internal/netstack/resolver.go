// SPDX-License-Identifier: GPL-3.0-or-later

package netstack

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/paths"
)

// Resolver performs DNS lookups using [SondaMeasurer].
//
// Use [NewResolver] to construct.
type Resolver struct {
	// Measurer is the measurement runner.
	//
	// Set by [NewResolver].
	Measurer *SondaMeasurer

	// ServerAddr is the DNS server address and port.
	//
	// Default: "8.8.8.8:53".
	ServerAddr string

	// Timeout is the timeout for each DNS query.
	//
	// Default: 5s.
	Timeout time.Duration
}

// NewResolver creates a [*Resolver] with sensible defaults.
func NewResolver(measurer *SondaMeasurer) *Resolver {
	return &Resolver{
		Measurer:   measurer,
		ServerAddr: "8.8.8.8:53",
		Timeout:    5 * time.Second,
	}
}

// LookupHost resolves a domain name and returns both IPv4 and IPv6 addresses.
func (rx *Resolver) LookupHost(ctx context.Context, domain string) ([]string, error) {
	// We run a sequential lookup since this is a batch tool.
	addrsA, errA := rx.LookupA(ctx, domain)
	addrsAAAA, errAAAA := rx.LookupAAAA(ctx, domain)

	// Merge the addrs and determin whether we succeeded.
	addrs := append(addrsA, addrsAAAA...)
	if len(addrs) <= 0 {
		err := errors.Join(errA, errAAAA)
		runtimex.Assert(err != nil)
		return nil, err
	}
	return addrs, nil
}

// LookupA resolves a domain name and returns the addresses.
func (rx *Resolver) LookupA(ctx context.Context, domain string) ([]string, error) {
	spanDir, err := rx.Measurer.Run(ctx, &SondaMeasureDNSOverUDP{
		Domain:    domain,
		QueryType: "A",
		Target:    rx.ServerAddr,
		Timeout:   rx.Timeout,
	})
	if err != nil {
		return nil, err
	}
	return readResponseAddrs(spanDir, "responseA")
}

// LookupAAAA resolves a domain name and returns the IPv6 addresses.
func (rx *Resolver) LookupAAAA(ctx context.Context, domain string) ([]string, error) {
	spanDir, err := rx.Measurer.Run(ctx, &SondaMeasureDNSOverUDP{
		Domain:    domain,
		QueryType: "AAAA",
		Target:    rx.ServerAddr,
		Timeout:   rx.Timeout,
	})
	if err != nil {
		return nil, err
	}
	return readResponseAddrs(spanDir, "responseAAAA")
}

// readResponseAddrs reads stdout.txt from a span directory and extracts
// addresses from lines matching the given message name.
func readResponseAddrs(spanDir, msgName string) ([]string, error) {
	filep, err := os.Open(paths.SpanStdout(spanDir))
	if err != nil {
		return nil, err
	}
	defer filep.Close()

	var addrs []string
	scanner := bufio.NewScanner(filep)
	for scanner.Scan() {
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry["msg"] != msgName {
			continue
		}
		values, ok := entry["value"].([]any)
		if !ok {
			continue
		}
		for _, v := range values {
			if s, ok := v.(string); ok {
				addrs = append(addrs, s)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if len(addrs) <= 0 {
		return nil, fmt.Errorf("no %s records found", msgName)
	}
	return addrs, nil
}
