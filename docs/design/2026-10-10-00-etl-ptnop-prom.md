---
author: sbs
status: active
---

# Design: `sonda-etl-ptnop-prom`

## Purpose

Export ptnop spans as Prometheus metrics, replacing the duration
stats of `etl-ptnop-qoe` and adding failure and I/O counts.

## How it works

- Long-running service serving `http://127.0.0.1:9774/metrics`.
Prometheus cannot scrape a Unix domain socket.

- Every 30 s it drains the work units in `/run/sonda/etl-ptnop-prom`
(see `2026-10-09-00-triggers.md`).

- Each span flattens into one record: ptnop Done events carry the
whole pipeline context (later stages emit `ESKIP` with the intended
SNI, URL, and query), so we do not need the scan configuration.

## Metrics

- `sonda_ptnop_spans_total`, labeled with `failed_at`, `err_class`,
and `http_status`: which stage fails, and how.

- One duration histogram per stage, observed only on success: these
replace the QoE pipeline.

- `sonda_ptnop_io_bytes_total`: how much network I/O the probe
costs. Read from `closeDone`, below TLS, so it is a lower bound on
wire bytes.

- `sonda_slice_*` (cgroup): resource usage of sonda as a whole, since
except for this service sonda consists of short-lived processes.

## Labels

- Destination labels (protocol, address, SNI, URL, query) attribute
performance and failures to a measurement: one address may fail while
another works.

- Reflexive address labels tell when the network changed. Spans that
run before STUN have them empty, and we keep them: when STUN fails,
hiding them would hide the failing scans.

## Known limitations

- Counters live in memory and reset on restart; `rate()` and
`increase()` handle that. Lost work units mean missed increments.

- `client_golang` never forgets a label set, so series for long-gone
addresses accumulate. If this grows, a daily `Reset()` (or
`RuntimeMaxSec=1d`) is the simplest fix.
