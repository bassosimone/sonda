// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/ptnopclient"
	"github.com/bassosimone/sonda/internal/ptnoprpc"
	"github.com/bassosimone/sonda/internal/ptnopspool"
)

// lookupHost resolves a domain name using 8.8.8.8:53/udp.
//
// We honor the "optimize" setting of the calling step.
func lookupHost(ctx context.Context, conn *ptnopclient.Conn,
	state *sharedState, with map[string]string, domain string) ([]string, error) {
	a, errA := lookupA(ctx, conn, state, with, domain)
	aaaa, errAAAA := lookupAAAA(ctx, conn, state, with, domain)
	if errA != nil && errAAAA != nil {
		return nil, errors.Join(errA, errAAAA)
	}
	out := append(a, aaaa...)
	runtimex.Assert(len(out) > 0)
	return out, nil
}

func newLookupRequest(state *sharedState,
	with map[string]string, domain, queryType string) *ptnoprpc.Request {
	return &ptnoprpc.Request{
		AddrPort:     "8.8.8.8:53",
		DNSQueryName: domain,
		DNSQueryType: queryType,
		Optimize:     with["optimize"],
		Pipeline:     "dns-over-udp",
		Tags:         state.Tags(),
		Timeout:      5 * time.Second,
	}
}

// lookupA resolves a domain name to A using 8.8.8.8:53/udp.
func lookupA(ctx context.Context, conn *ptnopclient.Conn,
	state *sharedState, with map[string]string, domain string) ([]string, error) {
	spanDir, err := state.RunAndSave(ctx, conn, newLookupRequest(state, with, domain, "A"))
	if err != nil {
		return nil, err
	}
	return ptnopspool.ResolvedAddrsA(spanDir)
}

// lookupAAAA resolves a domain name to AAAA using 8.8.8.8:53/udp.
func lookupAAAA(ctx context.Context, conn *ptnopclient.Conn,
	state *sharedState, with map[string]string, domain string) ([]string, error) {
	v6sup := state.SupportsV6()
	if v6sup != nil && !*v6sup {
		return nil, errors.New("no answer") // same suffix as the stdlib error for EAI_NODATA
	}
	spanDir, err := state.RunAndSave(ctx, conn, newLookupRequest(state, with, domain, "AAAA"))
	if err != nil {
		return nil, err
	}
	return ptnopspool.ResolvedAddrsAAAA(spanDir)
}
