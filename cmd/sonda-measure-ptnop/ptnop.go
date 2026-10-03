// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"

	"github.com/bassosimone/ptnop"
	"github.com/bassosimone/runtimex"
	"github.com/pion/stun/v3"
)

func ptnopRunPipeline(ctx context.Context, input *pipelineInput) int {
	// 1. Create the shared pipeline configuration.
	cfg := ptnop.NewConfig()
	cfg.SLogger = input.logger
	cfg.Dialer = input.env.Dialer

	// 2. Do something different depending on the pipeline name.
	switch input.name {
	case "dns-over-https":
		runtimex.Assert(input.tlsConfig != nil)
		runtimex.Assert(input.httpReq != nil)
		return ptnopRunDNS(ctx, input, ptnop.Compose6(
			ptnop.NewConnectFunc(cfg, "tcp"),
			ptnop.NewObserveConnFunc(cfg),
			ptnop.NewCancelWatchFunc(),
			ptnop.NewTLSHandshakeFunc(cfg, input.tlsConfig),
			ptnop.NewHTTPConnFunc(cfg),
			ptnop.NewDNSOverHTTPSConnFunc(cfg, input.httpReq.URL.String()),
		))

	case "dns-over-tcp":
		return ptnopRunDNS(ctx, input, ptnop.Compose4(
			ptnop.NewConnectFunc(cfg, "tcp"),
			ptnop.NewObserveConnFunc(cfg),
			ptnop.NewCancelWatchFunc(),
			ptnop.NewDNSOverTCPConnFunc(cfg),
		))

	case "dns-over-tls":
		runtimex.Assert(input.tlsConfig != nil)
		return ptnopRunDNS(ctx, input, ptnop.Compose5(
			ptnop.NewConnectFunc(cfg, "tcp"),
			ptnop.NewObserveConnFunc(cfg),
			ptnop.NewCancelWatchFunc(),
			ptnop.NewTLSHandshakeFunc(cfg, input.tlsConfig),
			ptnop.NewDNSOverTLSConnFunc(cfg),
		))

	case "dns-over-udp":
		return ptnopRunDNS(ctx, input, ptnop.Compose4(
			ptnop.NewConnectFunc(cfg, "udp"),
			ptnop.NewObserveConnFunc(cfg),
			ptnop.NewCancelWatchFunc(),
			ptnop.NewDNSOverUDPConnFunc(cfg),
		))

	case "http":
		return ptnopRunHTTP(ctx, input, ptnop.Compose4(
			ptnop.NewConnectFunc(cfg, "tcp"),
			ptnop.NewObserveConnFunc(cfg),
			ptnop.NewCancelWatchFunc(),
			ptnop.NewHTTPConnFunc(cfg),
		))

	case "https":
		runtimex.Assert(input.tlsConfig != nil)
		return ptnopRunHTTP(ctx, input, ptnop.Compose5(
			ptnop.NewConnectFunc(cfg, "tcp"),
			ptnop.NewObserveConnFunc(cfg),
			ptnop.NewCancelWatchFunc(),
			ptnop.NewTLSHandshakeFunc(cfg, input.tlsConfig),
			ptnop.NewHTTPConnFunc(cfg),
		))

	case "tcp":
		result := ptnop.Compose3(
			ptnop.NewConnectFunc(cfg, "tcp"),
			ptnop.NewObserveConnFunc(cfg),
			ptnop.NewCancelWatchFunc(),
		).Call(ctx, input.addrPort)
		if result.Err != nil {
			return logFailure(input.logger, "tcpPipeline", result.Err, 1)
		}
		result.V.Close()
		return 0

	case "tls":
		runtimex.Assert(input.tlsConfig != nil)
		result := ptnop.Compose4(
			ptnop.NewConnectFunc(cfg, "tcp"),
			ptnop.NewObserveConnFunc(cfg),
			ptnop.NewCancelWatchFunc(),
			ptnop.NewTLSHandshakeFunc(cfg, input.tlsConfig),
		).Call(ctx, input.addrPort)
		if result.Err != nil {
			return logFailure(input.logger, "tlsPipeline", result.Err, 1)
		}
		result.V.Close()
		return 0

	case "stun":
		return ptnopRunSTUN(ctx, input, ptnop.Compose3(
			ptnop.NewConnectFunc(cfg, "udp"),
			ptnop.NewObserveConnFunc(cfg),
			ptnop.NewCancelWatchFunc(),
		))

	default:
		return logUsageError(input.logger, "selectPipeline", errUnknownPipeline(input.name))
	}
}

// ptnopRunHTTP dials, performs the round trip, and drains the body.
func ptnopRunHTTP(ctx context.Context, input *pipelineInput,
	dialFunc ptnop.Func[netip.AddrPort, *ptnop.HTTPConn]) int {
	// 1. Dial the HTTP connection.
	httpConn := dialFunc.Call(ctx, input.addrPort)
	defer httpConn.Close()

	// 2. Perform the HTTP round trip.
	runtimex.Assert(input.httpReq != nil)
	resp, err := httpConn.RoundTrip(input.httpReq)
	if err != nil {
		return logFailure(input.logger, "roundTrip", err, 1)
	}
	defer resp.Body.Close()

	// 3. Drain the response body.
	if _, err := io.Copy(input.bodyFp, resp.Body); err != nil {
		return logFailure(input.logger, "readBody", err, 1)
	}
	return 0
}

// ptnopRunDNS dials and performs the DNS exchange.
func ptnopRunDNS(ctx context.Context, input *pipelineInput,
	dialFunc ptnop.Func[netip.AddrPort, ptnop.DNSConn]) int {
	// 1. Dial the DNS connection.
	dnsConn := dialFunc.Call(ctx, input.addrPort)
	defer dnsConn.Close()

	// 2. Perform the DNS exchange.
	resp, err := dnsConn.Exchange(ctx, input.query)
	if err != nil {
		return logFailure(input.logger, "exchange", err, 1)
	}

	// 3. Print records as expected by `./internal/ptnopspool`.
	//
	// TODO(bassosimone): we may want to support logging other response types.
	if cnames, err := resp.RecordsCNAME(); err == nil {
		input.logger.Info("sondaDnsRecordsCNAME", slog.Any("dnsRecordsList", cnames))
	}
	if addrs, err := resp.RecordsA(); err == nil {
		input.logger.Info("sondaDnsRecordsA", slog.Any("dnsRecordsList", addrs))
	}
	if addrs, err := resp.RecordsAAAA(); err == nil {
		input.logger.Info("sondaDnsRecordsAAAA", slog.Any("dnsRecordsList", addrs))
	}
	return 0
}

// ptnopRunSTUN dials and performs the STUN binding transaction.
func ptnopRunSTUN(ctx context.Context, input *pipelineInput,
	dialFunc ptnop.Func[netip.AddrPort, ptnop.Result[net.Conn]]) int {
	// 1. Dial the UDP connection.
	res := dialFunc.Call(ctx, input.addrPort)
	if err := res.Err; err != nil {
		return logFailure(input.logger, "dialStun", err, 1)
	}
	conn := res.V
	defer conn.Close()

	// 2. Build STUN binding request using pion/stun as codec.
	req := stun.MustBuild(stun.TransactionID, stun.BindingRequest)

	// 3. Write the binding request.
	if _, err := conn.Write(req.Raw); err != nil {
		return logFailure(input.logger, "writeStunBindingRequest", err, 1)
	}

	// 4. Read the binding response.
	buf := make([]byte, 1024)
	bytesRead, err := conn.Read(buf)
	if err != nil {
		return logFailure(input.logger, "readStunBindingResponse", err, 1)
	}

	// 5. Decode the response using pion/stun as codec.
	resp := new(stun.Message)
	resp.Raw = buf[:bytesRead]
	if err := resp.Decode(); err != nil {
		return logFailure(input.logger, "decodeStunBindingResponse", err, 1)
	}
	if reqTID, respTID := req.TransactionID, resp.TransactionID; reqTID != respTID {
		err := fmt.Errorf("stun: transaction ID mismatch: expected %q, got %q", reqTID, respTID)
		return logFailure(input.logger, "decodeStunBindingResponse", err, 1)
	}
	if rt := resp.Type; rt != stun.BindingSuccess {
		err := fmt.Errorf("stun: expected stun.BindingSuccess, got %v", rt)
		return logFailure(input.logger, "decodeStunBindingResponse", err, 1)
	}

	// 6. Extract and log the reflexive address.
	var xorAddr stun.XORMappedAddress
	if err := xorAddr.GetFrom(resp); err != nil {
		return logFailure(input.logger, "decodeStunBindingResponse", err, 1)
	}
	input.logger.Info("stunBindingResult",
		slog.String("stunReflexiveAddr", xorAddr.IP.String()),
		slog.Int("stunReflexivePort", xorAddr.Port),
	)
	return 0
}
