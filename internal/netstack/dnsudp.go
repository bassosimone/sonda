// SPDX-License-Identifier: GPL-3.0-or-later

package netstack

import (
	"context"
	"time"
)

// DNSOverUDPTransport performs DNS lookups over UDP using [SondaMeasurer].
//
// Use [NewDNSOverUDPTransport] to construct.
type DNSOverUDPTransport struct {
	// Measurer is the measurement runner.
	//
	// Set by [NewDNSOverUDPTransport].
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

// NewDNSOverUDPTransport creates a [*DNSOverUDPTransport] with sensible defaults.
func NewDNSOverUDPTransport(measurer *SondaMeasurer) *DNSOverUDPTransport {
	return &DNSOverUDPTransport{
		Measurer:   measurer,
		ServerAddr: "8.8.8.8:53",
		Timeout:    5 * time.Second,
	}
}

// LookupA resolves a domain name and returns the IPv4 addresses.
func (t *DNSOverUDPTransport) LookupA(ctx context.Context, domain string) ([]string, error) {
	spanDir, err := t.Measurer.Run(ctx, &SondaMeasureDNSOverUDP{
		Domain:    domain,
		QueryType: "A",
		Target:    t.ServerAddr,
		Timeout:   t.Timeout,
	})
	if err != nil {
		return nil, err
	}
	return readResponseAddrs(spanDir, "responseA")
}

// LookupAAAA resolves a domain name and returns the IPv6 addresses.
func (t *DNSOverUDPTransport) LookupAAAA(ctx context.Context, domain string) ([]string, error) {
	spanDir, err := t.Measurer.Run(ctx, &SondaMeasureDNSOverUDP{
		Domain:    domain,
		QueryType: "AAAA",
		Target:    t.ServerAddr,
		Timeout:   t.Timeout,
	})
	if err != nil {
		return nil, err
	}
	return readResponseAddrs(spanDir, "responseAAAA")
}
