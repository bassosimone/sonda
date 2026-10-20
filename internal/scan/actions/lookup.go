// SPDX-License-Identifier: GPL-3.0-or-later

package actions

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/ptnoprpc"
	"github.com/bassosimone/sonda/internal/ptnopspool"
)

// lookupHost is the internal entry point for resolving domain names.
func (r *WorkflowRunner) lookupHost(
	ctx context.Context,
	conn WorkflowPtnopConn,
	domain string,
	logger *slog.Logger,
	optimize string,
) ([]string, error) {
	a, errA := r.lookupA(ctx, conn, domain, logger, optimize)
	aaaa, errAAAA := r.lookupAAAA(ctx, conn, domain, logger, optimize)
	if errA != nil && errAAAA != nil {
		return nil, errors.Join(errA, errAAAA)
	}
	out := append(a, aaaa...)
	runtimex.Assert(len(out) > 0)
	return out, nil
}

func (r *WorkflowRunner) newLookupRequest(
	domain string, optimize string, queryType string) *ptnoprpc.Request {
	return &ptnoprpc.Request{
		AddrPort:     "8.8.8.8:53",
		DNSQueryName: domain,
		DNSQueryType: queryType,
		Optimize:     optimize,
		Pipeline:     "dns-over-udp",
		Tags:         r.tags(),
		Timeout:      5 * time.Second,
	}
}

func (r *WorkflowRunner) lookupA(ctx context.Context, conn WorkflowPtnopConn,
	domain string, logger *slog.Logger, optimize string) ([]string, error) {
	result, err := r.runPtnop(ctx, conn, logger, r.newLookupRequest(domain, optimize, "A"))
	if err != nil {
		return nil, err
	}
	return ptnopspool.ResolvedAddrsA(result.SpanDir)
}

func (r *WorkflowRunner) lookupAAAA(ctx context.Context, conn WorkflowPtnopConn,
	domain string, logger *slog.Logger, optimize string) ([]string, error) {
	v6sup := r.supportsV6()
	if v6sup != nil && !*v6sup {
		return nil, errors.New("no answer") // same suffix as the stdlib error for EAI_NODATA
	}
	result, err := r.runPtnop(ctx, conn, logger, r.newLookupRequest(domain, optimize, "AAAA"))
	if err != nil {
		return nil, err
	}
	return ptnopspool.ResolvedAddrsAAAA(result.SpanDir)
}
