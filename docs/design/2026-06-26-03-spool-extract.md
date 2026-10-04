---
author: sbs
status: active
---

# Design: `sonda etl-ptnop-qoe extract`

## Purpose

Extracts Parquet metrics from structured log spans. Each span
directory gets its own `qoe.parquet` file containing one row
per completed network operation. Go extracts, Python analyzes.

It lives in the `etl-ptnop-qoe` plugin rather than in `spool`
because parsing ptnop events and choosing the QoE metrics are
not spool concerns. The plugin owns the metric schema and the
per-span file names.

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

A single flat struct (`metricsRow`, private to the plugin) with
required columns for fields present on every `*Done` event and
nullable columns for fields that are conditional on event type or
session context:

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

- Writes go to `qoe.parquet.tmp`, then `os.Rename` to
  `qoe.parquet`. Consumers only see complete files.

- If `qoe.parquet` already exists, the span is skipped.
  Running extract twice produces the same result. This makes
  it safe to run periodically and to run by hand at any time.

## Per-span file names

The output and sentinel files that a pipeline adds to a span
directory depend on the pipeline. We name them after the
pipeline's destination data type, which is `qoe` here:

- `qoe.parquet` is the extract output and the sentinel that
  tells `extract` the span is already processed.

- `qoe.parquet.tmp` is the extract output while being written.

- `qoe.loaded` is the sentinel written by `load`.

This allows several pipelines to read the same span. For
example, a hypothetical `sonda-etl-ptnop-foo` would write
`foo.parquet` and `foo.loaded` into the same `ptnop` span
directory without interfering with `qoe.*`. Two pipelines
with the same destination but different sources never share a
span directory, so the destination name alone is enough.

## How it walks

Same sharding tree walk that `sonda spool gc` uses for each
data type (depth-3 descent through `XXXX/X/X/<spanID>/`). Skips `.tmp` directories
(incomplete spans) and spans whose UUIDv7 timestamp is older
than `--max-age`.

## Flags

- `--spool-dir DIR` — root of the spool tree (default: `.`).
- `--max-age DURATION` — only extract spans newer than this
  (default: `6h`).

## Scheduling

The Debian package runs extract, followed by load, from its own
systemd timer (`sonda-etl-ptnop-qoe.timer`), one hour after each
run finishes. The service passes no flags, so the `6h` window
covers several missed runs. GC runs from its own timer and removes
spans older than `6h` by default, so extract normally processes
a span long before GC could remove it.

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
