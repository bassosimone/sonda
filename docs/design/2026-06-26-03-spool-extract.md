---
author: sbs
status: active
---

# Design: `sonda spool extract`

## Purpose

Extracts Parquet metrics from structured log spans. Each span
directory gets its own `metrics.parquet` file containing one row
per completed network operation. Go extracts, Python analyzes.

## What gets extracted

The command filters for `*Done` events — the subset of structured
log events that carry timing and error information for a completed
operation:

- `connectDone` — TCP (and UDP) connection attempt.
- `tlsHandshakeDone` — TLS negotiation.
- `httpRoundTripDone` — HTTP request/response cycle.
- `dnsExchangeDone` — DNS query/response exchange.

All other events (start markers, I/O spans, wire observations,
notifications) are skipped. The rationale: `*Done` events carry
`t0`, `t`, and `errClass` — the minimum needed for latency and
error analysis.

## Parquet schema

A single flat struct (`structured.Metrics`) with required columns
for fields present on every `*Done` event and nullable columns for
fields that are conditional on event type or session context:

| Column                      | Type   | Nullable | Present on              |
|-----------------------------|--------|----------|-------------------------|
| `span_id`                   | string | no       | all                     |
| `msg`                       | string | no       | all                     |
| `t0`                        | int64  | no       | all (microsecond ts)    |
| `t`                         | int64  | no       | all (microsecond ts)    |
| `duration_us`               | int64  | no       | all (computed)          |
| `local_addr`                | string | no       | all                     |
| `remote_addr`               | string | no       | all                     |
| `protocol`                  | string | no       | all                     |
| `err_class`                 | string | yes      | failures only           |
| `server_protocol`           | string | yes      | dnsExchangeDone         |
| `http_response_status_code` | int64  | yes      | httpRoundTripDone       |
| `reflexive_addr_v4`         | string | yes      | when STUN tag present   |
| `reflexive_addr_v6`         | string | yes      | when STUN tag present   |

Column names use `snake_case` (Parquet/pandas convention). Timestamps
are microseconds since epoch, matching Parquet's native timestamp
resolution. Duration is computed as `t - t0` to avoid float
precision issues that would arise from millisecond fractions.

Nullable columns use pointer types in Go. Data scientists prefer
null over sentinel values (empty string, zero) because pandas
operations like `groupby`, `count`, and `notna()` handle null
correctly by default.

## Atomicity and idempotency

- Writes go to `metrics.parquet.tmp`, then `os.Rename` to
  `metrics.parquet`. Consumers only see complete files.

- If `metrics.parquet` already exists, the span is skipped.
  Running extract twice produces the same result. This makes
  it safe to invoke from `sonda scan` on every cycle.

## How it walks

Same sharding tree walk as `sonda spool gc` (depth-3 descent
through `XXXX/X/X/<spanID>/`). Skips `.tmp` directories
(incomplete spans) and spans whose UUIDv7 timestamp is older
than `--max-age`.

## Flags

- `--spool-dir DIR` — root of the spool tree (default: `.`).
- `--max-age DURATION` — only extract spans newer than this
  (default: `6h`).

## Integration with `sonda scan`

`sonda scan` invokes `sonda spool extract` as a subprocess
before garbage collection, with `--max-age 1h`. This window
covers the spans created during the current scan cycle.
Extract runs before GC to ensure metrics are written before
spans could be removed.

## Lambda-per-span, not global aggregation

Each span gets its own Parquet file. There is no cross-span
merging, partitioning, or time-bucketing in this command. A
separate aggregation step (not yet built) can merge per-span
files into larger datasets when needed. This separation keeps
extraction simple and idempotent.

## What this is not

- Not an analysis tool. It writes Parquet; pandas reads it.
- Not a retention policy. Parquet files live inside the span
  directory and are removed when GC removes the span.
- Not a pipeline. There is no streaming, batching, or
  deduplication. Each span is processed independently.
