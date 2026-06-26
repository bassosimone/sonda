// SPDX-License-Identifier: GPL-3.0-or-later

package netstack

import (
	"context"
	"time"
)

// DNSOverHTTPSTransport performs DNS lookups over HTTPS using [SondaMeasurer].
//
// Use [NewDNSOverHTTPSTransport] to construct.
type DNSOverHTTPSTransport struct {
	// Measurer is the measurement runner.
	//
	// Set by [NewDNSOverHTTPSTransport].
	Measurer *SondaMeasurer

	// HTTPHost is the HTTP Host header value.
	//
	// Default: "dns.google".
	HTTPHost string

	// SNI is the TLS Server Name Indication.
	//
	// Default: "dns.google".
	SNI string

	// ServerAddr is the DNS server address and port.
	//
	// Default: "8.8.8.8:443".
	ServerAddr string

	// Timeout is the timeout for each DNS query.
	//
	// Default: 5s.
	Timeout time.Duration

	// URLPath is the DoH URL path.
	//
	// Default: "/dns-query".
	URLPath string
}

// NewDNSOverHTTPSTransport creates a [*DNSOverHTTPSTransport] with sensible defaults.
func NewDNSOverHTTPSTransport(measurer *SondaMeasurer) *DNSOverHTTPSTransport {
	return &DNSOverHTTPSTransport{
		Measurer:   measurer,
		HTTPHost:   "dns.google",
		SNI:        "dns.google",
		ServerAddr: "8.8.8.8:443",
		Timeout:    5 * time.Second,
		URLPath:    "/dns-query",
	}
}

// LookupA resolves a domain name and returns the IPv4 addresses.
func (t *DNSOverHTTPSTransport) LookupA(ctx context.Context, domain string) ([]string, error) {
	spanDir, err := t.Measurer.Run(ctx, &SondaMeasureDNSOverHTTPS{
		Domain:    domain,
		HTTPHost:  t.HTTPHost,
		QueryType: "A",
		SNI:       t.SNI,
		Target:    t.ServerAddr,
		Timeout:   t.Timeout,
		URLPath:   t.URLPath,
	})
	if err != nil {
		return nil, err
	}
	return readResponseAddrs(spanDir, "sondaDnsRecordsA")
}

// LookupAAAA resolves a domain name and returns the IPv6 addresses.
func (t *DNSOverHTTPSTransport) LookupAAAA(ctx context.Context, domain string) ([]string, error) {
	spanDir, err := t.Measurer.Run(ctx, &SondaMeasureDNSOverHTTPS{
		Domain:    domain,
		HTTPHost:  t.HTTPHost,
		QueryType: "AAAA",
		SNI:       t.SNI,
		Target:    t.ServerAddr,
		Timeout:   t.Timeout,
		URLPath:   t.URLPath,
	})
	if err != nil {
		return nil, err
	}
	return readResponseAddrs(spanDir, "sondaDnsRecordsAAAA")
}
