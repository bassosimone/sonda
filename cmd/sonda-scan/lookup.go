// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/ptnoprpc"
)

// lookupHost resolves a domain name using 8.8.8.8:53/udp.
func lookupHost(ctx context.Context, client *ptnoprpc.Client,
	state *sharedState, domain string) ([]string, error) {
	a, errA := lookupA(ctx, client, state, domain)
	aaaa, errAAAA := lookupAAAA(ctx, client, state, domain)
	if errA != nil && errAAAA != nil {
		return nil, errors.Join(errA, errAAAA)
	}
	out := append(a, aaaa...)
	runtimex.Assert(len(out) > 0)
	return out, nil
}

func newLookupRequest(state *sharedState, domain, queryType string) *ptnoprpc.Request {
	return &ptnoprpc.Request{
		AddrPort:     "8.8.8.8:53",
		DNSQueryName: domain,
		DNSQueryType: queryType,
		Pipeline:     "dns-over-udp",
		Tags:         state.Tags(),
		Timeout:      5 * time.Second,
	}
}

// lookupA resolves a domain name to A using 8.8.8.8:53/udp.
func lookupA(ctx context.Context, client *ptnoprpc.Client,
	state *sharedState, domain string) ([]string, error) {
	spanDir, err := state.RunAndSave(ctx, client, newLookupRequest(state, domain, "A"))
	if err != nil {
		return nil, err
	}
	return spanDir.ResolvedAddrsA()
}

// lookupAAAA resolves a domain name to AAAA using 8.8.8.8:53/udp.
func lookupAAAA(ctx context.Context, client *ptnoprpc.Client,
	state *sharedState, domain string) ([]string, error) {
	spanDir, err := state.RunAndSave(ctx, client, newLookupRequest(state, domain, "AAAA"))
	if err != nil {
		return nil, err
	}
	return spanDir.ResolvedAddrsAAAA()
}
